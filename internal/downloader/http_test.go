package downloader

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/abzcoding/hget/internal/state"
	"github.com/abzcoding/hget/internal/ui"
	"github.com/abzcoding/hget/internal/util"
)

func TestPartCalculate(t *testing.T) {
	ui.DisplayProgress = false

	originalDataFolder := state.DataFolder
	state.DataFolder = ".hget_test/"
	defer func() {
		state.DataFolder = originalDataFolder
		usr, _ := user.Current()
		testFolder := filepath.Join(usr.HomeDir, state.DataFolder)
		os.RemoveAll(testFolder)
	}()

	testCases := []struct {
		parts       int64
		totalSize   int64
		url         string
		expectParts int
	}{
		{10, 100, "http://foo.bar/file", 10},
		{5, 1000, "http://example.com/largefile", 5},
		{1, 50, "http://test.org/smallfile", 1},
		{3, 10, "http://tiny.file/data", 3},
	}

	for _, tc := range testCases {
		parts := PartCalculate(tc.parts, tc.totalSize, tc.url)

		if len(parts) != tc.expectParts {
			t.Errorf("Expected %d parts, got %d", tc.expectParts, len(parts))
		}

		for i, part := range parts {
			if part.URL != tc.url {
				t.Errorf("Part %d: Expected URL %s, got %s", i, tc.url, part.URL)
			}

			if part.Index != int64(i) {
				t.Errorf("Part %d: Expected Index %d, got %d", i, i, part.Index)
			}

			expectedSize := tc.totalSize / tc.parts
			if i < int(tc.parts-1) {
				if part.RangeFrom != expectedSize*int64(i) {
					t.Errorf("Part %d: Expected RangeFrom %d, got %d",
						i, expectedSize*int64(i), part.RangeFrom)
				}
				if part.RangeTo != expectedSize*int64(i+1)-1 {
					t.Errorf("Part %d: Expected RangeTo %d, got %d",
						i, expectedSize*int64(i+1)-1, part.RangeTo)
				}
			} else {
				if part.RangeFrom != expectedSize*int64(i) {
					t.Errorf("Part %d: Expected RangeFrom %d, got %d",
						i, expectedSize*int64(i), part.RangeFrom)
				}
				if part.RangeTo != tc.totalSize {
					t.Errorf("Part %d: Expected RangeTo %d, got %d",
						i, tc.totalSize, part.RangeTo)
				}
			}

			usr, _ := user.Current()
			expectedBasePath := filepath.Join(usr.HomeDir, state.DataFolder)
			if !strings.Contains(part.Path, expectedBasePath) {
				t.Errorf("Part %d: Path does not contain expected base path: %s", i, part.Path)
			}

			fileName := filepath.Base(part.Path)
			expectedPrefix := util.TaskFromURL(tc.url) + ".part"
			if !strings.HasPrefix(fileName, expectedPrefix) {
				t.Errorf("Part %d: Expected filename prefix %s, got %s",
					i, expectedPrefix, fileName)
			}
		}
	}
}

func TestProxyAwareHTTPClient(t *testing.T) {
	client := ProxyAwareHTTPClient("", false, 15*time.Second)
	if client == nil {
		t.Fatal("ProxyAwareHTTPClient returned nil with no proxy")
	}

	httpProxyClient := ProxyAwareHTTPClient("http://localhost:8080", false, 15*time.Second)
	if httpProxyClient == nil {
		t.Fatal("ProxyAwareHTTPClient returned nil with HTTP proxy")
	}

	socksProxyClient := ProxyAwareHTTPClient("localhost:1080", false, 15*time.Second)
	if socksProxyClient == nil {
		t.Fatal("ProxyAwareHTTPClient returned nil with SOCKS proxy")
	}

	tlsClient := ProxyAwareHTTPClient("", true, 15*time.Second)
	if tlsClient == nil {
		t.Fatal("ProxyAwareHTTPClient returned nil with TLS skip verification")
	}
}

func TestHandleCompletedPart(t *testing.T) {
	ui.DisplayProgress = false

	part := state.Part{
		Index:     0,
		URL:       "http://example.com/test",
		Path:      "test.part000000",
		RangeFrom: 100,
		RangeTo:   100,
	}

	fileChan := make(chan string, 1)
	stateSaveChan := make(chan state.Part, 1)

	dl := &HTTPDownloader{
		url:       "http://example.com/test",
		file:      "test",
		par:       1,
		len:       100,
		parts:     []state.Part{part},
		resumable: true,
	}

	dl.handleCompletedPart(part, fileChan, stateSaveChan)

	select {
	case path := <-fileChan:
		if path != part.Path {
			t.Errorf("Expected path %q to be sent to fileChan, got %q", part.Path, path)
		}
	default:
		t.Errorf("Expected path to be sent to fileChan")
	}

	select {
	case savedPart := <-stateSaveChan:
		if savedPart.Index != part.Index ||
			savedPart.URL != part.URL ||
			savedPart.Path != part.Path ||
			savedPart.RangeFrom != part.RangeFrom ||
			savedPart.RangeTo != part.RangeTo {
			t.Errorf("Saved part does not match original part")
		}
	default:
		t.Errorf("No part sent to stateSaveChan")
	}
}

func TestBuildRequestForPart(t *testing.T) {
	testCases := []struct {
		description string
		part        state.Part
		contentLen  int64
		parallelism int64
		expected    string
	}{
		{
			description: "Single connection download (no range)",
			part: state.Part{
				Index:     0,
				URL:       "http://example.com/file",
				Path:      "file.part000000",
				RangeFrom: 0,
				RangeTo:   100,
			},
			contentLen:  100,
			parallelism: 1,
			expected:    "",
		},
		{
			description: "Multiple connection download with middle part",
			part: state.Part{
				Index:     1,
				URL:       "http://example.com/file",
				Path:      "file.part000001",
				RangeFrom: 50,
				RangeTo:   99,
			},
			contentLen:  200,
			parallelism: 3,
			expected:    "bytes=50-99",
		},
		{
			description: "Multiple connection download with last part",
			part: state.Part{
				Index:     2,
				URL:       "http://example.com/file",
				Path:      "file.part000002",
				RangeFrom: 100,
				RangeTo:   200,
			},
			contentLen:  200,
			parallelism: 3,
			expected:    "bytes=100-",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			dl := &HTTPDownloader{
				url:       tc.part.URL,
				file:      "file",
				par:       tc.parallelism,
				len:       tc.contentLen,
				parts:     []state.Part{tc.part},
				resumable: true,
			}

			req, err := dl.buildRequestForPart(context.Background(), tc.part)
			if err != nil {
				t.Fatalf("buildRequestForPart failed: %v", err)
			}

			rangeHeader := req.Header.Get("Range")
			if tc.expected == "" {
				if rangeHeader != "" {
					t.Errorf("Expected no Range header, got '%s'", rangeHeader)
				}
			} else {
				if rangeHeader != tc.expected {
					t.Errorf("Expected Range header '%s', got '%s'", tc.expected, rangeHeader)
				}
			}

			if req.URL.String() != tc.part.URL {
				t.Errorf("Expected URL %s, got %s", tc.part.URL, req.URL.String())
			}
		})
	}
}

func TestCopyContent(t *testing.T) {
	testData := "This is test data for copy content"

	t.Run("No Rate Limit", func(t *testing.T) {
		src := strings.NewReader(testData)
		var dst strings.Builder

		dl := &HTTPDownloader{rate: 0}

		if err := dl.copyContent(src, &dst); err != nil {
			t.Fatalf("copyContent error: %v", err)
		}

		if dst.String() != testData {
			t.Errorf("Expected content '%s', got '%s'", testData, dst.String())
		}
	})

	t.Run("With Rate Limit", func(t *testing.T) {
		src := strings.NewReader(testData)
		var dst strings.Builder

		dl := &HTTPDownloader{rate: 1024}

		errCh := make(chan error, 1)
		go func() { errCh <- dl.copyContent(src, &dst) }()

		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("copyContent error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("Copy with rate limit timed out")
		}

		if dst.String() != testData {
			t.Errorf("Expected content '%s', got '%s'", testData, dst.String())
		}
	})
}

func TestCopyContentWithSharedLimiter(t *testing.T) {
	testData := "shared limiter content"
	src := strings.NewReader(testData)
	var dst strings.Builder

	dl := &HTTPDownloader{
		sharedLimiter: rate.NewLimiter(rate.Limit(1<<20), 1<<20),
	}

	if err := dl.copyContent(src, &dst); err != nil {
		t.Fatalf("copyContent error: %v", err)
	}

	if dst.String() != testData {
		t.Errorf("Expected content '%s', got '%s'", testData, dst.String())
	}
}

func TestProxyAwareHTTPClientConfiguration(t *testing.T) {
	client := ProxyAwareHTTPClient("http://127.0.0.1:3128", false, 15*time.Second)
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport is not *http.Transport")
	}
	if tr.Proxy == nil {
		t.Errorf("expected HTTP proxy to be configured on Transport.Proxy")
	}

	client = ProxyAwareHTTPClient("127.0.0.1:1080", false, 15*time.Second)
	tr, ok = client.Transport.(*http.Transport)
	if !ok || tr.DialContext == nil {
		t.Errorf("expected SOCKS5 DialContext to be configured")
	}

	if tr.TLSHandshakeTimeout == 0 || tr.ResponseHeaderTimeout == 0 || tr.ExpectContinueTimeout == 0 {
		t.Errorf("expected transport timeouts to be set")
	}
}

func TestNewHTTPDownloaderProbe(t *testing.T) {
	ui.DisplayProgress = false

	originalDataFolder := state.DataFolder
	state.DataFolder = ".hget_test/"
	defer func() {
		state.DataFolder = originalDataFolder
		usr, _ := user.Current()
		os.RemoveAll(filepath.Join(usr.HomeDir, state.DataFolder))
	}()

	content := strings.Repeat("x", 1024)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("Content-Length", "1024")
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(content))
		}
	})
	ts := httptest.NewServer(h)
	defer ts.Close()

	d := NewHTTPDownloader(ts.URL, 4, false, "", "", 15*time.Second)
	if d.par != 4 {
		t.Fatalf("expected par=4, got %d", d.par)
	}
	if d.len != 1024 {
		t.Fatalf("expected len=1024, got %d", d.len)
	}
	if !d.resumable {
		t.Fatalf("expected resumable=true")
	}
	if len(d.parts) != 4 {
		t.Fatalf("expected 4 parts, got %d", len(d.parts))
	}
}

func TestNewHTTPDownloaderRangeFallback(t *testing.T) {
	ui.DisplayProgress = false

	originalDataFolder := state.DataFolder
	state.DataFolder = ".hget_test/"
	defer func() {
		state.DataFolder = originalDataFolder
		usr, _ := user.Current()
		os.RemoveAll(filepath.Join(usr.HomeDir, state.DataFolder))
	}()

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			if rng := r.Header.Get("Range"); strings.HasPrefix(rng, "bytes=") {
				w.Header().Set("Content-Range", "bytes 0-0/4096")
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write([]byte("x"))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(strings.Repeat("x", 4096)))
		}
	})
	ts := httptest.NewServer(h)
	defer ts.Close()

	d := NewHTTPDownloader(ts.URL, 4, false, "", "", 15*time.Second)
	if d.par != 4 {
		t.Fatalf("expected par=4, got %d", d.par)
	}
	if d.len != 4096 {
		t.Fatalf("expected len=4096, got %d", d.len)
	}
}
