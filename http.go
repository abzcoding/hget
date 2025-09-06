package main

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
	"github.com/fatih/color"
	"github.com/fujiwara/shapeio"
	"golang.org/x/net/proxy"
	"golang.org/x/time/rate"
	pb "gopkg.in/cheggaaa/pb.v1"
)

var (
	acceptRangeHeader   = "Accept-Ranges"
	contentLengthHeader = "Content-Length"
)

const defaultUserAgent = "curl/8.7.1"

// Default response header timeout; can be overridden by CLI flag in main.go
var responseHeaderTimeout = 15 * time.Second

// HTTPDownloader holds the required configurations
type HTTPDownloader struct {
	proxy         string
	rate          int64
	url           string
	file          string
	par           int64
	len           int64
	ips           []string
	skipTLS       bool
	parts         []Part
	resumable     bool
	sharedLimiter *rate.Limiter
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

// NewHTTPDownloader returns a ProxyAwareHttpClient with given configurations.
func NewHTTPDownloader(url string, par int, skipTLS bool, proxyServer string, bwLimit string) *HTTPDownloader {
	var resumable = true
	client := ProxyAwareHTTPClient(proxyServer, skipTLS)

	parsed, err := stdurl.Parse(url)
	FatalCheck(err)

	ips, err := net.LookupIP(parsed.Hostname())
	FatalCheck(err)

	ipstr := FilterIPV4(ips)
	Printf("Resolve ip: %s\n", strings.Join(ipstr, " | "))

	// Probe capabilities with HEAD, fallback to range GET
	var rangeSupported bool
	var lenValue int64
	lengthSpecified := false

	// HEAD probe
	req, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		FatalCheck(err)
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", defaultUserAgent)
	resp, err := client.Do(req)
	if err == nil {
		defer resp.Body.Close()
		if strings.Contains(strings.ToLower(resp.Header.Get(acceptRangeHeader)), "bytes") {
			rangeSupported = true
		}
		if cl := resp.Header.Get(contentLengthHeader); cl != "" {
			if v, perr := strconv.ParseInt(cl, 10, 64); perr == nil && v > 0 {
				lenValue = v
				lengthSpecified = true
			}
		}
	}

	// Range GET probe (0-0) to robustly detect range support and total size via Content-Range
	if !rangeSupported || lenValue == 0 {
		preq, perr := http.NewRequest("GET", url, nil)
		if perr == nil {
			preq.Header.Set("Range", "bytes=0-0")
			preq.Header.Set("Accept", "*/*")
			preq.Header.Set("User-Agent", defaultUserAgent)
			pr, derr := client.Do(preq)
			if derr == nil {
				defer pr.Body.Close()
				if pr.StatusCode == http.StatusPartialContent {
					rangeSupported = true
					// Parse Content-Range: bytes 0-0/12345
					cr := pr.Header.Get("Content-Range")
					if slash := strings.LastIndex(cr, "/"); slash != -1 && slash+1 < len(cr) {
						if total, aerr := strconv.ParseInt(cr[slash+1:], 10, 64); aerr == nil && total > 0 {
							lenValue = total
							lengthSpecified = true
						}
					}
				} else {
					// Treat as non-ranged; attempt to read Content-Length if present
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
		Printf("Target url does not confirm range support, fallback to parallel 1\n")
		par = 1
	}

	if lenValue == 0 {
		Printf("Target url did not provide content length, fallback to parallel 1\n")
		lenValue = 1 // progress bar does not accept 0 length
		par = 1
		resumable = false
	}

	Printf("Start download with %d connections \n", par)

	sizeInMb := float64(lenValue) / (1024 * 1024)
	if !lengthSpecified {
		Printf("Download size: not specified\n")
	} else if sizeInMb < 1024 {
		Printf("Download target size: %.1f MB\n", sizeInMb)
	} else {
		Printf("Download target size: %.1f GB\n", sizeInMb/1024)
	}

	file := TaskFromURL(url)

	ret := new(HTTPDownloader)
	ret.rate = 0
	bandwidthLimit, err := units.ParseStrictBytes(bwLimit)
	if err == nil {
		ret.rate = bandwidthLimit // bytes per second
		Printf("Download with bandwidth limit set to %s[%d]\n", bwLimit, ret.rate)
	}
	ret.url = url
	ret.file = file
	ret.par = int64(par)
	ret.len = lenValue
	ret.ips = ipstr
	ret.skipTLS = skipTLS
	ret.parts = partCalculate(int64(par), lenValue, url)
	ret.resumable = resumable
	ret.proxy = proxyServer
	if ret.rate > 0 {
		ret.sharedLimiter = rate.NewLimiter(rate.Limit(ret.rate), int(ret.rate))
	}

	return ret
}

// partCalculate splits the download into parts.
func partCalculate(par int64, len int64, url string) []Part {
	ret := make([]Part, par)
	for j := int64(0); j < par; j++ {
		from := (len / par) * j
		var to int64
		if j < par-1 {
			to = (len/par)*(j+1) - 1
		} else {
			to = len
		}

		file := TaskFromURL(url)

		folder := FolderOf(url)
		if err := MkdirIfNotExist(folder); err != nil {
			Errorf("%v", err)
			os.Exit(1)
		}

		// Padding 0 before path name as filename will be sorted as strings
		fname := fmt.Sprintf("%s.part%06d", file, j)
		path := filepath.Join(folder, fname) // e.g. ~/.hget/download-file-name/part-name
		ret[j] = Part{Index: j, URL: url, Path: path, RangeFrom: from, RangeTo: to}
	}
	return ret
}

// ProxyAwareHTTPClient returns an HTTP client that may use an HTTP or SOCKS5 proxy.
func ProxyAwareHTTPClient(proxyServer string, skipTLS bool) *http.Client {
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}

	// Mimic curl/OpenSSL defaults for TLS1.2 suites and curve preferences.
	tlsConf := &tls.Config{
		InsecureSkipVerify:       skipTLS, // #nosec G402
		MinVersion:               tls.VersionTLS12,
		PreferServerCipherSuites: true, // server-side only, harmless on client
		CurvePreferences: []tls.CurveID{
			tls.X25519, tls.CurveP256, tls.CurveP384, tls.CurveP521,
		},
		CipherSuites: []uint16{
			// TLS 1.2 order similar to curl/openssl
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
		TLSClientConfig:       tlsConf,
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: responseHeaderTimeout,
		ExpectContinueTimeout: 2 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		DisableCompression:    false,
		ForceAttemptHTTP2:     true,
		// let default TLSNextProto enable HTTP/2
	}

	httpClient := &http.Client{Transport: httpTransport}

	// Preserve headers across redirects
	httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 {
			// Copy headers from the initial request if missing
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
func (d *HTTPDownloader) Do(doneChan chan bool, fileChan chan string, errorChan chan error, interruptChan chan bool, stateSaveChan chan Part) {
	var wg sync.WaitGroup
	var bars []*pb.ProgressBar

	// Start each part via helper functions.
	for _, p := range d.parts {
		// If the part is already completed or has no data to download.
		if p.RangeTo <= p.RangeFrom {
			d.handleCompletedPart(p, fileChan, stateSaveChan)
			continue
		}

		var bar *pb.ProgressBar
		if DisplayProgressBar() {
			bar = pb.New64(p.RangeTo - p.RangeFrom).
				SetUnits(pb.U_BYTES).
				Prefix(color.YellowString(fmt.Sprintf("%s-%d", d.file, p.Index)))
			bars = append(bars, bar)
		}

		wg.Add(1)
		go d.downloadPart(p, bar, &wg, fileChan, errorChan, interruptChan, stateSaveChan)
	}

	// Start the progress bar pool if needed.
	if DisplayProgressBar() && len(bars) > 0 {
		pool, err := pb.StartPool(bars...)
		FatalCheck(err)
		wg.Wait()
		doneChan <- true
		pool.Stop()
	} else {
		wg.Wait()
		doneChan <- true
	}
}

// handleCompletedPart simply notifies that a part doesn't need downloading.
func (d *HTTPDownloader) handleCompletedPart(p Part, fileChan chan string, stateSaveChan chan Part) {
	// Avoid sending path here; we'll assemble paths from final states in main
	// Send a part indicating no additional data
	stateSaveChan <- Part{
		Index:     p.Index,
		URL:       d.url,
		Path:      p.Path,
		RangeFrom: p.RangeFrom,
		RangeTo:   p.RangeTo,
	}
}

// downloadPart handles the download process for an individual part.
func (d *HTTPDownloader) downloadPart(part Part, bar *pb.ProgressBar, wg *sync.WaitGroup,
	fileChan chan string, errorChan chan error, interruptChan chan bool, stateSaveChan chan Part) {
	defer wg.Done()

	// Context for cancellation via interrupt
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		// cancel on interrupt
		<-interruptChan
		cancel()
	}()

	// Build and issue the HTTP request.
	req, err := d.buildRequestForPart(ctx, part)
	if err != nil {
		errorChan <- err
		return
	}

	client := ProxyAwareHTTPClient(d.proxy, d.skipTLS)
	resp, err := client.Do(req)
	if err != nil {
		// treat context cancellation as interrupt, not error
		if errors.Is(err, context.Canceled) || (ctx.Err() != nil) {
			// Save current state (no progress yet)
			stateSaveChan <- Part{
				Index:     part.Index,
				URL:       d.url,
				Path:      part.Path,
				RangeFrom: part.RangeFrom,
				RangeTo:   part.RangeTo,
			}
			return
		}
		errorChan <- err
		return
	}
	defer resp.Body.Close()

	// Open the file for appending data.
	f, err := os.OpenFile(part.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		Errorf("%v\n", err)
		errorChan <- err
		return
	}
	defer f.Close()

	// Combine file writer and progress bar writer if needed.
	var writer io.Writer
	if DisplayProgressBar() && bar != nil {
		writer = io.MultiWriter(f, bar)
	} else {
		writer = f
	}

	// Start copying content in a goroutine.
	current := int64(0)
	finishDownloadChan := make(chan bool)
	go d.copyContent(resp.Body, writer, finishDownloadChan)

	// Wait for either an interrupt event or a successful completion.
	select {
	case <-ctx.Done():
		// Close the response body to terminate download.
		resp.Body.Close()
		<-finishDownloadChan // wait for cleanup
	case <-finishDownloadChan:
		// Download finished normally.
	}

	// Update part state and notify fileChan.
	currentPart := Part{
		Index:     part.Index,
		URL:       d.url,
		Path:      part.Path,
		RangeFrom: part.RangeFrom + current, // update the downloaded offset
		RangeTo:   part.RangeTo,
	}
	stateSaveChan <- currentPart
	fileChan <- part.Path

	// Finalize progress bar if used.
	if DisplayProgressBar() && bar != nil {
		// Give a small moment for the bar to update.
		time.Sleep(100 * time.Millisecond)
		bar.Finish()
	}
}

// buildRequestForPart prepares the HTTP GET request for a given part including the Range header.
func (d *HTTPDownloader) buildRequestForPart(ctx context.Context, part Part) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", d.url, nil)
	if err != nil {
		return nil, err
	}

	// Add Range header if downloading in parallel.
	if d.par > 1 {
		var ranges string
		// If RangeTo is not the very end, use an explicit range.
		if part.RangeTo != d.len {
			ranges = fmt.Sprintf("bytes=%d-%d", part.RangeFrom, part.RangeTo)
		} else {
			// Otherwise, download until the end.
			ranges = fmt.Sprintf("bytes=%d-", part.RangeFrom)
		}
		req.Header.Add("Range", ranges)
	}
	// curl-compatible defaults
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", defaultUserAgent)

	return req, nil
}

// copyContent copies data from the response to the writer with an optional weigthed rate limiter.
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

// DebugProbe performs HEAD and a 0-0 Range GET to print diagnostics without downloading.
func DebugProbe(url string, skipTLS bool, proxyServer string) {
	client := ProxyAwareHTTPClient(proxyServer, skipTLS)

	Printf("Probing URL: %s\n", url)

	// HEAD
	headReq, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		Errorf("HEAD build error: %v\n", err)
		return
	}
	headReq.Header.Set("Accept-Encoding", "identity")
	headReq.Header.Set("User-Agent", defaultUserAgent)

	headResp, err := client.Do(headReq)
	if err != nil {
		Errorf("HEAD request error: %v\n", err)
	} else {
		defer headResp.Body.Close()
		Printf("HEAD status: %s\n", headResp.Status)
		Printf("HEAD Accept-Ranges: %s\n", headResp.Header.Get(acceptRangeHeader))
		Printf("HEAD Content-Length: %s\n", headResp.Header.Get(contentLengthHeader))
		Printf("HEAD Content-Type: %s\n", headResp.Header.Get("Content-Type"))
	}

	// Range GET 0-0
	rangeReq, err := http.NewRequest("GET", url, nil)
	if err != nil {
		Errorf("Range build error: %v\n", err)
		return
	}
	rangeReq.Header.Set("Range", "bytes=0-0")
	rangeReq.Header.Set("Accept-Encoding", "identity")
	rangeReq.Header.Set("User-Agent", defaultUserAgent)

	rangeResp, err := client.Do(rangeReq)
	if err != nil {
		Errorf("Range request error: %v\n", err)
		return
	}
	defer rangeResp.Body.Close()
	Printf("Range GET status: %s\n", rangeResp.Status)
	Printf("Range Content-Range: %s\n", rangeResp.Header.Get("Content-Range"))
	Printf("Range Content-Length: %s\n", rangeResp.Header.Get(contentLengthHeader))

	// Evaluate
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

	Printf("Detected Range support: %v\n", rangeSupported)
	if totalLen > 0 {
		Printf("Detected Content-Length: %d\n", totalLen)
	} else {
		Printf("Detected Content-Length: unknown\n")
	}
}
