package main

import (
	"crypto/tls"
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
	pb "gopkg.in/cheggaaa/pb.v1"
)

var (
	tr = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client = &http.Client{Transport: tr}
)

var (
	acceptRangeHeader   = "Accept-Ranges"
	contentLengthHeader = "Content-Length"
)

// HTTPDownloader holds the required configurations
type HTTPDownloader struct {
	proxy     string
	rate      int64
	url       string
	file      string
	par       int64
	len       int64
	ips       []string
	skipTLS   bool
	parts     []Part
	resumable bool
}

// NewHTTPDownloader returns a ProxyAwareHttpClient with given configurations.
func NewHTTPDownloader(url string, par int, skipTLS bool, proxyServer string, bwLimit string) *HTTPDownloader {
	var resumable = true
	client := ProxyAwareHTTPClient(proxyServer)

	parsed, err := stdurl.Parse(url)
	FatalCheck(err)

	ips, err := net.LookupIP(parsed.Host)
	FatalCheck(err)

	ipstr := FilterIPV4(ips)
	Printf("Resolve ip: %s\n", strings.Join(ipstr, " | "))

	req, err := http.NewRequest("GET", url, nil)
	FatalCheck(err)

	resp, err := client.Do(req)
	FatalCheck(err)

	if resp.Header.Get(acceptRangeHeader) == "" {
		Printf("Target url is not supported range download, fallback to parallel 1\n")
		par = 1
	}

	// Get download range
	clen := resp.Header.Get(contentLengthHeader)
	if clen == "" {
		Printf("Target url not contain Content-Length header, fallback to parallel 1\n")
		clen = "1" // set 1 because progress bar does not accept 0 length
		par = 1
		resumable = false
	}

	Printf("Start download with %d connections \n", par)

	lenValue, err := strconv.ParseInt(clen, 10, 64)
	FatalCheck(err)

	sizeInMb := float64(lenValue) / (1024 * 1024)
	if clen == "1" {
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
		ret.rate = bandwidthLimit
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
func ProxyAwareHTTPClient(proxyServer string) *http.Client {
	httpTransport := &http.Transport{}
	httpClient := &http.Client{Transport: httpTransport}
	var dialer proxy.Dialer = proxy.Direct

	if len(proxyServer) > 0 {
		if strings.HasPrefix(proxyServer, "http") {
			proxyURL, err := stdurl.Parse(proxyServer)
			if err != nil {
				fmt.Fprintln(os.Stderr, "invalid proxy: ", err)
			}
			dialer, err = proxy.FromURL(proxyURL, proxy.Direct)
			if err == nil {
				httpTransport.Dial = dialer.Dial
			}
		} else {
			dialer, err := proxy.SOCKS5("tcp", proxyServer, nil, proxy.Direct)
			if err == nil {
				httpTransport.Dial = dialer.Dial
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
	fileChan <- p.Path
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

	// Build and issue the HTTP request.
	req, err := d.buildRequestForPart(part)
	if err != nil {
		errorChan <- err
		return
	}

	client := ProxyAwareHTTPClient(d.proxy)
	resp, err := client.Do(req)
	if err != nil {
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
	case <-interruptChan:
		// An interrupt is received; close the response body to terminate download.
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
func (d *HTTPDownloader) buildRequestForPart(part Part) (*http.Request, error) {
	req, err := http.NewRequest("GET", d.url, nil)
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

	return req, nil
}

// copyContent copies data from the response to the writer with an optional weigthed rate limiter.
func (d *HTTPDownloader) copyContent(src io.Reader, dst io.Writer, done chan bool) {
	defer func() { done <- true }()
	if d.rate != 0 {
		reader := shapeio.NewReader(src)
		reader.SetRateLimit(float64(d.rate))
		_, _ = io.Copy(dst, reader)
	} else {
		_, _ = io.Copy(dst, src)
	}
}
