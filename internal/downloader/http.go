package downloader

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	stdurl "net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alecthomas/units"
	"github.com/fujiwara/shapeio"
	"golang.org/x/net/proxy"
	"golang.org/x/time/rate"

	"github.com/abzcoding/hget/internal/state"
	"github.com/abzcoding/hget/internal/ui"
	"github.com/abzcoding/hget/internal/util"
)

var (
	acceptRangeHeader   = "Accept-Ranges"
	contentLengthHeader = "Content-Length"
)

const defaultUserAgent = "curl/8.7.1"

// HTTPDownloader holds the required configurations.
type HTTPDownloader struct {
	proxy         string
	rate          int64
	url           string
	file          string
	par           int64
	len           int64
	ips           []string
	skipTLS       bool
	parts         []state.Part
	resumable     bool
	sharedLimiter *rate.Limiter
	timeout       time.Duration
	// client is reused across all part goroutines for connection pooling.
	client *http.Client
}

// rateLimitedReader wraps a reader and throttles reads using a shared limiter.
type rateLimitedReader struct {
	r   io.Reader
	lim *rate.Limiter
}

func (rlr *rateLimitedReader) Read(p []byte) (int, error) {
	n, err := rlr.r.Read(p)
	if n > 0 {
		_ = rlr.lim.WaitN(context.Background(), n)
	}
	return n, err
}

// NewHTTPDownloader returns a configured HTTPDownloader ready to download url.
func NewHTTPDownloader(url string, par int, skipTLS bool, proxyServer string, bwLimit string, timeout time.Duration) *HTTPDownloader {
	var resumable = true
	client := ProxyAwareHTTPClient(proxyServer, skipTLS, timeout)

	parsed, err := stdurl.Parse(url)
	util.FatalCheck(err)

	ips, err := net.LookupIP(parsed.Hostname())
	util.FatalCheck(err)

	ipstr := util.FilterIPV4(ips)
	ui.Printf("Resolved IP: %s\n", strings.Join(ipstr, " | "))

	// Probe capabilities with HEAD, fallback to range GET.
	var rangeSupported bool
	var lenValue int64
	lengthSpecified := false

	// HEAD probe
	req, err := http.NewRequest("HEAD", url, nil)
	util.FatalCheck(err)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Accept-Encoding", "identity")
	var headResp *http.Response
	headResp, err = client.Do(req)
	if err == nil {
		defer headResp.Body.Close()
		if strings.Contains(strings.ToLower(headResp.Header.Get(acceptRangeHeader)), "bytes") {
			rangeSupported = true
		}
		if cl := headResp.Header.Get(contentLengthHeader); cl != "" {
			if v, perr := strconv.ParseInt(cl, 10, 64); perr == nil && v > 0 {
				lenValue = v
				lengthSpecified = true
			}
		}
	}

	// Range GET probe (0-0) to robustly detect range support and total size via Content-Range.
	if !rangeSupported || lenValue == 0 {
		preq, perr := http.NewRequest("GET", url, nil)
		if perr == nil {
			preq.Header.Set("Range", "bytes=0-0")
			preq.Header.Set("Accept", "*/*")
			preq.Header.Set("User-Agent", defaultUserAgent)
			preq.Header.Set("Accept-Encoding", "identity")
			pr, derr := client.Do(preq)
			if derr == nil {
				defer pr.Body.Close()
				if pr.StatusCode == http.StatusPartialContent {
					rangeSupported = true
					cr := pr.Header.Get("Content-Range")
					if slash := strings.LastIndex(cr, "/"); slash != -1 && slash+1 < len(cr) {
						if total, aerr := strconv.ParseInt(cr[slash+1:], 10, 64); aerr == nil && total > 0 {
							lenValue = total
							lengthSpecified = true
						}
					}
				} else {
					rangeSupported = false
					if cl := pr.Header.Get(contentLengthHeader); cl != "" {
						if v, perr := strconv.ParseInt(cl, 10, 64); perr == nil && v > 0 {
							lenValue = v
							lengthSpecified = true
						}
					}
				}
			}
		}
	}

	if !rangeSupported {
		ui.Printf("Server does not support range requests, using 1 connection\n")
		par = 1
	}

	if lenValue == 0 {
		ui.Printf("Content-Length unknown, using 1 connection\n")
		lenValue = 1 // progress bar does not accept 0 length
		par = 1
		resumable = false
	}

	ui.Printf("Connections: %d\n", par)

	sizeInMb := float64(lenValue) / (1024 * 1024)
	if !lengthSpecified {
		ui.Printf("Size: unknown\n")
	} else if sizeInMb < 1024 {
		ui.Printf("Size: %.1f MB\n", sizeInMb)
	} else {
		ui.Printf("Size: %.1f GB\n", sizeInMb/1024)
	}

	file := util.TaskFromURL(url)

	ret := new(HTTPDownloader)
	ret.rate = 0
	bandwidthLimit, err := units.ParseStrictBytes(bwLimit)
	if err == nil {
		ret.rate = bandwidthLimit
		ui.Printf("Bandwidth limit: %s (%d B/s)\n", bwLimit, ret.rate)
	}
	ret.url = url
	ret.file = file
	ret.par = int64(par)
	ret.len = lenValue
	ret.ips = ipstr
	ret.skipTLS = skipTLS
	ret.parts = PartCalculate(int64(par), lenValue, url)
	ret.resumable = resumable
	ret.proxy = proxyServer
	ret.timeout = timeout
	ret.client = client
	if ret.rate > 0 {
		ret.sharedLimiter = rate.NewLimiter(rate.Limit(ret.rate), int(ret.rate))
	}

	// Notify the TUI that download metadata is ready.
	if ui.Program != nil {
		ui.Program.Send(ui.DownloadStartMsg{
			URL:      url,
			FileName: file,
			Size:     lenValue,
			NumParts: par,
			IPs:      ipstr,
		})
	}

	return ret
}

// progressWriter tracks bytes written and forwards progress updates to the TUI.
type progressWriter struct {
	partIndex  int
	downloaded int64
	total      int64
	writer     io.Writer
	lastSent   time.Time
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.writer.Write(p)
	pw.downloaded += int64(n)
	if ui.Program != nil {
		now := time.Now()
		if now.Sub(pw.lastSent) >= 80*time.Millisecond || pw.downloaded >= pw.total {
			ui.Program.Send(ui.PartProgressMsg{
				Index:      pw.partIndex,
				Downloaded: pw.downloaded,
				Total:      pw.total,
			})
			pw.lastSent = now
		}
	}
	return n, err
}

// PartCalculate splits the download into parts.
func PartCalculate(par int64, length int64, url string) []state.Part {
	ret := make([]state.Part, par)
	for j := int64(0); j < par; j++ {
		from := (length / par) * j
		var to int64
		if j < par-1 {
			to = (length/par)*(j+1) - 1
		} else {
			to = length
		}

		file := util.TaskFromURL(url)

		folder := state.FolderOf(url)
		if err := util.MkdirIfNotExist(folder); err != nil {
			ui.Errorf("%v", err)
			os.Exit(1)
		}

		// Zero-pad part index so filenames sort lexicographically in order.
		fname := fmt.Sprintf("%s.part%06d", file, j)
		path := filepath.Join(folder, fname)
		ret[j] = state.Part{Index: j, URL: url, Path: path, RangeFrom: from, RangeTo: to}
	}
	return ret
}

// ProxyAwareHTTPClient returns an HTTP client that may use an HTTP or SOCKS5 proxy.
func ProxyAwareHTTPClient(proxyServer string, skipTLS bool, timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}

	tlsConf := &tls.Config{
		InsecureSkipVerify: skipTLS, // #nosec G402
		MinVersion:         tls.VersionTLS12,
		CurvePreferences: []tls.CurveID{
			tls.X25519, tls.CurveP256, tls.CurveP384, tls.CurveP521,
		},
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
			tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
			tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
		},
		NextProtos: []string{"h2", "http/1.1"},
	}

	httpTransport := &http.Transport{
		TLSClientConfig: tlsConf,
		DialContext:     dialer.DialContext,
		// Disable transparent decompression so range byte offsets are never
		// reinterpreted against a compressed stream.
		DisableCompression:    true,
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: timeout,
		ExpectContinueTimeout: 2 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   32,
		ForceAttemptHTTP2:     true,
	}

	httpClient := &http.Client{Transport: httpTransport}

	httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 {
			for k, v := range via[0].Header {
				if req.Header.Get(k) == "" {
					req.Header[k] = v
				}
			}
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}

	if len(proxyServer) > 0 {
		if strings.HasPrefix(proxyServer, "http") {
			proxyURL, err := stdurl.Parse(proxyServer)
			if err != nil {
				fmt.Fprintln(os.Stderr, "invalid proxy: ", err)
			} else {
				httpTransport.Proxy = http.ProxyURL(proxyURL)
			}
		} else {
			// assume SOCKS5
			if dialer, err := proxy.SOCKS5("tcp", proxyServer, nil, proxy.Direct); err == nil {
				httpTransport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
					return dialer.Dial(network, addr)
				}
			}
		}
	}
	return httpClient
}

// Do is the main download entry point. It dispatches the download to multiple parts.
func (d *HTTPDownloader) Do(doneChan chan bool, fileChan chan string, errorChan chan error, interruptChan chan bool, stateSaveChan chan state.Part) {
	var wg sync.WaitGroup

	for _, p := range d.parts {
		if p.RangeTo <= p.RangeFrom {
			// Part is fully downloaded; satisfy both channels the drain loop expects.
			d.handleCompletedPart(p, fileChan, stateSaveChan)
			continue
		}
		wg.Add(1)
		go d.downloadPart(p, &wg, fileChan, errorChan, interruptChan, stateSaveChan)
	}

	wg.Wait()
	doneChan <- true
}

// handleCompletedPart notifies both drain channels that a part needs no downloading.
func (d *HTTPDownloader) handleCompletedPart(p state.Part, fileChan chan string, stateSaveChan chan state.Part) {
	stateSaveChan <- state.Part{
		Index:     p.Index,
		URL:       d.url,
		Path:      p.Path,
		RangeFrom: p.RangeFrom,
		RangeTo:   p.RangeTo,
	}
	fileChan <- p.Path
	if ui.Program != nil {
		ui.Program.Send(ui.PartDoneMsg{Index: int(p.Index)})
	}
}

// downloadPart handles the download process for an individual part.
func (d *HTTPDownloader) downloadPart(part state.Part, wg *sync.WaitGroup,
	fileChan chan string, errorChan chan error, interruptChan chan bool, stateSaveChan chan state.Part) {
	defer wg.Done()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Cancel context when an interrupt arrives; also exit the goroutine when
	// the context is cancelled by other means (e.g. normal completion via defer).
	go func() {
		select {
		case <-interruptChan:
			cancel()
		case <-ctx.Done():
		}
	}()

	req, err := d.buildRequestForPart(ctx, part)
	if err != nil {
		errorChan <- err
		return
	}

	// Reuse the shared client for connection pooling.
	resp, err := d.client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			stateSaveChan <- state.Part{
				Index:     part.Index,
				URL:       d.url,
				Path:      part.Path,
				RangeFrom: part.RangeFrom,
				RangeTo:   part.RangeTo,
			}
			fileChan <- part.Path
			return
		}
		errorChan <- err
		return
	}
	defer resp.Body.Close()

	f, err := os.OpenFile(part.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		ui.Errorf("%v\n", err)
		errorChan <- err
		return
	}
	defer f.Close()

	pw := &progressWriter{
		partIndex: int(part.Index),
		total:     part.RangeTo - part.RangeFrom,
		writer:    f,
		lastSent:  time.Now(),
	}

	finishDownloadChan := make(chan bool, 1)
	go d.copyContent(resp.Body, pw, finishDownloadChan)

	var interrupted bool
	select {
	case <-ctx.Done():
		interrupted = true
		resp.Body.Close()
		<-finishDownloadChan
	case <-finishDownloadChan:
	}

	// Save state: RangeFrom is updated to "next byte to download from".
	// Resume() uses this value directly without adding fi.Size() again.
	savedPart := state.Part{
		Index:     part.Index,
		URL:       d.url,
		Path:      part.Path,
		RangeFrom: part.RangeFrom + pw.downloaded,
		RangeTo:   part.RangeTo,
	}
	stateSaveChan <- savedPart
	fileChan <- part.Path

	if !interrupted {
		if ui.Program != nil {
			ui.Program.Send(ui.PartDoneMsg{Index: int(part.Index)})
		}
	}
}

// buildRequestForPart prepares the HTTP GET request for a given part.
func (d *HTTPDownloader) buildRequestForPart(ctx context.Context, part state.Part) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", d.url, nil)
	if err != nil {
		return nil, err
	}

	if d.par > 1 || part.RangeFrom > 0 {
		var rangeHdr string
		if part.RangeTo != d.len {
			// Explicit inclusive end byte.
			rangeHdr = fmt.Sprintf("bytes=%d-%d", part.RangeFrom, part.RangeTo)
		} else {
			// Open-ended: download until EOF.
			rangeHdr = fmt.Sprintf("bytes=%d-", part.RangeFrom)
		}
		req.Header.Add("Range", rangeHdr)
	}

	req.Header.Set("Accept", "*/*")
	// Prevent transparent gzip/br encoding which would invalidate byte offsets.
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", defaultUserAgent)

	return req, nil
}

// copyContent copies data from the response to the writer with optional rate limiting.
func (d *HTTPDownloader) copyContent(src io.Reader, dst io.Writer, done chan bool) {
	defer func() { done <- true }()
	if d.sharedLimiter != nil {
		_, _ = io.Copy(dst, &rateLimitedReader{r: src, lim: d.sharedLimiter})
	} else if d.rate != 0 {
		reader := shapeio.NewReader(src)
		reader.SetRateLimit(float64(d.rate))
		_, _ = io.Copy(dst, reader)
	} else {
		_, _ = io.Copy(dst, src)
	}
}

// NewHTTPDownloaderFromState rebuilds an HTTPDownloader from a saved State,
// applying the given connection settings. Used when resuming a download.
func NewHTTPDownloaderFromState(st *state.State, client *http.Client, proxyServer string, skipTLS bool, timeout time.Duration) *HTTPDownloader {
	return &HTTPDownloader{
		url:       st.URL,
		file:      util.TaskFromURL(st.URL),
		par:       int64(len(st.Parts)),
		len:       0, // unknown at resume time; open-ended range used for last part
		parts:     st.Parts,
		resumable: true,
		proxy:     proxyServer,
		skipTLS:   skipTLS,
		timeout:   timeout,
		client:    client,
	}
}

// NumParts returns the number of download parts.
func (d *HTTPDownloader) NumParts() int {
	return len(d.parts)
}

// IsResumable reports whether this download supports resumption.
func (d *HTTPDownloader) IsResumable() bool {
	return d.resumable
}

// DebugProbe performs HEAD and a 0-0 Range GET to print diagnostics without downloading.
func DebugProbe(url string, skipTLS bool, proxyServer string, timeout time.Duration) {
	client := ProxyAwareHTTPClient(proxyServer, skipTLS, timeout)

	ui.Printf("Probing URL: %s\n", url)

	headReq, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		ui.Errorf("HEAD build error: %v\n", err)
		return
	}
	headReq.Header.Set("Accept-Encoding", "identity")
	headReq.Header.Set("User-Agent", defaultUserAgent)

	var headResp *http.Response
	headResp, err = client.Do(headReq)
	if err != nil {
		ui.Errorf("HEAD request error: %v\n", err)
	} else {
		defer headResp.Body.Close()
		ui.Printf("HEAD status: %s\n", headResp.Status)
		ui.Printf("HEAD Accept-Ranges: %s\n", headResp.Header.Get(acceptRangeHeader))
		ui.Printf("HEAD Content-Length: %s\n", headResp.Header.Get(contentLengthHeader))
		ui.Printf("HEAD Content-Type: %s\n", headResp.Header.Get("Content-Type"))
	}

	rangeReq, err := http.NewRequest("GET", url, nil)
	if err != nil {
		ui.Errorf("Range build error: %v\n", err)
		return
	}
	rangeReq.Header.Set("Range", "bytes=0-0")
	rangeReq.Header.Set("Accept-Encoding", "identity")
	rangeReq.Header.Set("User-Agent", defaultUserAgent)

	rangeResp, err := client.Do(rangeReq)
	if err != nil {
		ui.Errorf("Range request error: %v\n", err)
		return
	}
	defer rangeResp.Body.Close()
	ui.Printf("Range GET status: %s\n", rangeResp.Status)
	ui.Printf("Range Content-Range: %s\n", rangeResp.Header.Get("Content-Range"))
	ui.Printf("Range Content-Length: %s\n", rangeResp.Header.Get(contentLengthHeader))

	var rangeSupported bool
	if rangeResp.StatusCode == http.StatusPartialContent {
		rangeSupported = true
	}

	var totalLen int64
	if cr := rangeResp.Header.Get("Content-Range"); cr != "" {
		if slash := strings.LastIndex(cr, "/"); slash != -1 && slash+1 < len(cr) {
			if total, err := strconv.ParseInt(cr[slash+1:], 10, 64); err == nil {
				totalLen = total
			}
		}
	}

	if totalLen == 0 && headResp != nil {
		if cl := headResp.Header.Get(contentLengthHeader); cl != "" {
			if v, err := strconv.ParseInt(cl, 10, 64); err == nil {
				totalLen = v
			}
		}
	}

	ui.Printf("Range support: %v\n", rangeSupported)
	if totalLen > 0 {
		ui.Printf("Content-Length: %d bytes (%.1f MB)\n", totalLen, float64(totalLen)/(1024*1024))
	} else {
		ui.Printf("Content-Length: unknown\n")
	}
}
