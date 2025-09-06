package main

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
)

func TestPartCalculate(t *testing.T) {
	// Disable progress bar for tests
	displayProgress = false

	// Setup test environment
	originalDataFolder := dataFolder
	dataFolder = ".hget_test/"
	defer func() {
		dataFolder = originalDataFolder
		usr, _ := user.Current()
		testFolder := filepath.Join(usr.HomeDir, dataFolder)
		os.RemoveAll(testFolder)
	}()

	// Test with different numbers of parts
	testCases := []struct {
		parts       int64
		totalSize   int64
		url         string
		expectParts int
	}{
		{10, 100, "http://foo.bar/file", 10},
		{5, 1000, "http://example.com/largefile", 5},
		{1, 50, "http://test.org/smallfile", 1},
		{3, 10, "http://tiny.file/data", 3}, // Small file, multiple parts
	}

	for _, tc := range testCases {
		parts := partCalculate(tc.parts, tc.totalSize, tc.url)

		// Check number of parts
		if len(parts) != tc.expectParts {
			t.Errorf("Expected %d parts, got %d", tc.expectParts, len(parts))
		}

		// Check part URLs
		for i, part := range parts {
			if part.URL != tc.url {
				t.Errorf("Part %d: Expected URL %s, got %s", i, tc.url, part.URL)
			}

			// Check part index
			if part.Index != int64(i) {
				t.Errorf("Part %d: Expected Index %d, got %d", i, i, part.Index)
			}

			// Check ranges
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
				// Last part might be larger due to division remainder
				if part.RangeFrom != expectedSize*int64(i) {
					t.Errorf("Part %d: Expected RangeFrom %d, got %d",
						i, expectedSize*int64(i), part.RangeFrom)
				}
				if part.RangeTo != tc.totalSize {
					t.Errorf("Part %d: Expected RangeTo %d, got %d",
						i, tc.totalSize, part.RangeTo)
				}
			}

			// Check path format
			usr, _ := user.Current()
			expectedBasePath := filepath.Join(usr.HomeDir, dataFolder)
			if !strings.Contains(part.Path, expectedBasePath) {
				t.Errorf("Part %d: Path does not contain expected base path: %s", i, part.Path)
			}

			fileName := filepath.Base(part.Path)
			expectedPrefix := TaskFromURL(tc.url) + ".part"
			if !strings.HasPrefix(fileName, expectedPrefix) {
				t.Errorf("Part %d: Expected filename prefix %s, got %s",
					i, expectedPrefix, fileName)
			}
		}
	}
}

func TestProxyAwareHTTPClient(t *testing.T) {
	// Test with no proxy
	client := ProxyAwareHTTPClient("", false)
	if client == nil {
		t.Fatal("ProxyAwareHTTPClient returned nil with no proxy")
	}

	// Cannot easily test with an actual proxy, but can verify it doesn't crash
	httpProxyClient := ProxyAwareHTTPClient("http://localhost:8080", false)
	if httpProxyClient == nil {
		t.Fatal("ProxyAwareHTTPClient returned nil with HTTP proxy")
	}

	socksProxyClient := ProxyAwareHTTPClient("localhost:1080", false)
	if socksProxyClient == nil {
		t.Fatal("ProxyAwareHTTPClient returned nil with SOCKS proxy")
	}

	// Test TLS skipVerify parameter
	tlsClient := ProxyAwareHTTPClient("", true)
	if tlsClient == nil {
		t.Fatal("ProxyAwareHTTPClient returned nil with TLS skip verification")
	}

	// Can't directly access TLS config, but it shouldn't crash
}

// Helper function to parse integers
func parseInt(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func TestHandleCompletedPart(t *testing.T) {
	// Disable progress bar for tests
	displayProgress = false

	// Create a part that's already complete
	part := Part{
		Index:     0,
		URL:       "http://example.com/test",
		Path:      "test.part000000",
		RangeFrom: 100, // RangeFrom equals RangeTo means no data to download
		RangeTo:   100,
	}

	// Create channels
	fileChan := make(chan string, 1)
	stateSaveChan := make(chan Part, 1)

	// Create downloader
	downloader := &HTTPDownloader{
		url:       "http://example.com/test",
		file:      "test",
		par:       1,
		len:       100,
		parts:     []Part{part},
		resumable: true,
	}

	// Handle the completed part
	downloader.handleCompletedPart(part, fileChan, stateSaveChan)

	// Verify the path was sent to fileChan
	select {
	case path := <-fileChan:
		if path != part.Path {
			t.Errorf("Expected path %s, got %s", part.Path, path)
		}
	default:
		t.Errorf("No path sent to fileChan")
	}

	// Verify the part was sent to stateSaveChan
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
	// Test cases for different range situations
	testCases := []struct {
		description string
		part        Part
		contentLen  int64
		parallelism int64
		expected    string
	}{
		{
			description: "Single connection download (no range)",
			part: Part{
				Index:     0,
				URL:       "http://example.com/file",
				Path:      "file.part000000",
				RangeFrom: 0,
				RangeTo:   100,
			},
			contentLen:  100,
			parallelism: 1,
			expected:    "", // No range header expected
		},
		{
			description: "Multiple connection download with middle part",
			part: Part{
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
			part: Part{
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
			// Create downloader
			downloader := &HTTPDownloader{
				url:       tc.part.URL,
				file:      "file",
				par:       tc.parallelism,
				len:       tc.contentLen,
				parts:     []Part{tc.part},
				resumable: true,
			}

			// Build request
			req, err := downloader.buildRequestForPart(context.Background(), tc.part)
			if err != nil {
				t.Fatalf("buildRequestForPart failed: %v", err)
			}

			// Check range header
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

			// Check URL
			if req.URL.String() != tc.part.URL {
				t.Errorf("Expected URL %s, got %s", tc.part.URL, req.URL.String())
			}
		})
	}
}

func TestCopyContent(t *testing.T) {
	// Create test data
	testData := "This is test data for copy content"

	// Test regular copy (no rate limit)
	t.Run("No Rate Limit", func(t *testing.T) {
		// Create source and destination
		src := strings.NewReader(testData)
		var dst strings.Builder

		// Create downloader with no rate limit
		downloader := &HTTPDownloader{
			rate: 0,
		}

		// Copy content
		done := make(chan bool)
		go downloader.copyContent(src, &dst, done)

		// Wait for completion
		<-done

		// Verify copied content
		if dst.String() != testData {
			t.Errorf("Expected content '%s', got '%s'", testData, dst.String())
		}
	})

	// Test copy with rate limit (can only test that it doesn't crash)
	t.Run("With Rate Limit", func(t *testing.T) {
		// Create source and destination
		src := strings.NewReader(testData)
		var dst strings.Builder

		// Create downloader with rate limit
		downloader := &HTTPDownloader{
			rate: 1024, // 1KB/s
		}

		// Copy content
		done := make(chan bool)
		go downloader.copyContent(src, &dst, done)

		// Wait for completion with timeout
		select {
		case <-done:
			// Success
		case <-time.After(2 * time.Second):
			t.Fatalf("Copy with rate limit timed out")
		}

		// Verify copied content
		if dst.String() != testData {
			t.Errorf("Expected content '%s', got '%s'", testData, dst.String())
		}
	})
}

func TestCopyContentWithSharedLimiter(t *testing.T) {
	// Verify copyContent path using sharedLimiter copies data correctly.
	testData := "shared limiter content"
	src := strings.NewReader(testData)
	var dst strings.Builder

	downloader := &HTTPDownloader{
		sharedLimiter: rate.NewLimiter(rate.Limit(1<<20), 1<<20), // high limit to avoid slowness
	}

	done := make(chan bool)
	go downloader.copyContent(src, &dst, done)
	<-done

	if dst.String() != testData {
		t.Errorf("Expected content '%s', got '%s'", testData, dst.String())
	}
}

func TestProxyAwareHTTPClientConfiguration(t *testing.T) {
	// HTTP proxy config should set Transport.Proxy
	client := ProxyAwareHTTPClient("http://127.0.0.1:3128", false)
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport is not *http.Transport")
	}
	if tr.Proxy == nil {
		t.Errorf("expected HTTP proxy to be configured on Transport.Proxy")
	}

	// SOCKS5 proxy config should set DialContext
	client = ProxyAwareHTTPClient("127.0.0.1:1080", false)
	tr, ok = client.Transport.(*http.Transport)
	if !ok || tr.DialContext == nil {
		t.Errorf("expected SOCKS5 DialContext to be configured")
	}

	// Ensure timeouts are set
	if tr.TLSHandshakeTimeout == 0 || tr.ResponseHeaderTimeout == 0 || tr.ExpectContinueTimeout == 0 {
		t.Errorf("expected transport timeouts to be set")
	}
}

func TestNewHTTPDownloaderProbe(t *testing.T) {
	// HEAD returns Accept-Ranges and Content-Length
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

	d := NewHTTPDownloader(ts.URL, 4, false, "", "")
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
	// HEAD has no Accept-Ranges/Content-Length. GET with Range returns 206 + Content-Range
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

	d := NewHTTPDownloader(ts.URL, 4, false, "", "")
	if d.par != 4 {
		t.Fatalf("expected par=4, got %d", d.par)
	}
	if d.len != 4096 {
		t.Fatalf("expected len=4096, got %d", d.len)
	}
}
