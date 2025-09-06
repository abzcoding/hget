package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// helper to create deterministic content
func makeContent(size int) []byte {
	data := make([]byte, size)
	for i := 0; i < size; i++ {
		data[i] = byte('A' + (i % 23))
	}
	return data
}

// start a configurable HTTP server
func startTestServer(t *testing.T, content []byte, supportRange bool, headShowsAcceptRanges bool, headHasContentLength bool, getHasContentLength bool, path string) (*httptest.Server, *int32) {
	var rangeRequests int32

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		switch r.Method {
		case http.MethodHead:
			if headShowsAcceptRanges && supportRange {
				w.Header().Set("Accept-Ranges", "bytes")
			}
			if headHasContentLength {
				w.Header().Set("Content-Length", strconv.Itoa(len(content)))
			}
			w.WriteHeader(http.StatusOK)
			return
		case http.MethodGet:
			// Range probe
			rangeHeader := r.Header.Get("Range")
			if supportRange && rangeHeader != "" && strings.HasPrefix(rangeHeader, "bytes=") {
				atomic.AddInt32(&rangeRequests, 1)
				// parse bytes=start-end
				spec := strings.TrimPrefix(rangeHeader, "bytes=")
				parts := strings.SplitN(spec, "-", 2)
				start, _ := strconv.ParseInt(parts[0], 10, 64)
				var end int64
				if parts[1] == "" {
					end = int64(len(content) - 1)
				} else {
					end, _ = strconv.ParseInt(parts[1], 10, 64)
				}
				if start < 0 {
					start = 0
				}
				if end >= int64(len(content)) {
					end = int64(len(content) - 1)
				}
				if start > end {
					w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
					return
				}
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(content)))
				if getHasContentLength {
					w.Header().Set("Content-Length", strconv.Itoa(int(end-start+1)))
				}
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write(content[start : end+1])
				return
			}
			// regular GET
			if getHasContentLength {
				w.Header().Set("Content-Length", strconv.Itoa(len(content)))
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(content)
			return
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts, &rangeRequests
}

func withTempCwd(t *testing.T) func() {
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	tdir := t.TempDir()
	if err := os.Chdir(tdir); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	return func() { _ = os.Chdir(old) }
}

func withTestDataFolder(t *testing.T) func() {
	original := dataFolder
	dataFolder = ".hget_e2e_test/"
	return func() {
		dataFolder = original
		usr, _ := user.Current()
		_ = os.RemoveAll(filepath.Join(usr.HomeDir, dataFolder))
	}
}

func TestE2EParallelDownloadWithRange(t *testing.T) {
	displayProgress = false
	restoreCwd := withTempCwd(t)
	defer restoreCwd()
	restoreDF := withTestDataFolder(t)
	defer restoreDF()

	content := makeContent(256 * 1024)
	path := "/file.bin"
	ts, _ := startTestServer(t, content, true, true, true, true, path)

	url := ts.URL + path
	Execute(url, nil, 4, false, "", "")

	// Verify output file exists and content matches
	out := TaskFromURL(url)
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("failed to read output: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("downloaded content mismatch")
	}

	// Ensure data folder cleaned up
	usr, _ := user.Current()
	folder := filepath.Join(usr.HomeDir, dataFolder, TaskFromURL(url))
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Fatalf("expected data folder removed, got err=%v", err)
	}
}

func TestE2EResumeDownload(t *testing.T) {
	displayProgress = false
	restoreCwd := withTempCwd(t)
	defer restoreCwd()
	restoreDF := withTestDataFolder(t)
	defer restoreDF()

	content := makeContent(300 * 1024)
	path := "/resume.bin"
	ts, _ := startTestServer(t, content, true, true, true, true, path)
	url := ts.URL + path

	// Prepare partial state files inside the expected folder
	parts := partCalculate(4, int64(len(content)), url)
	folder := FolderOf(url)
	if err := MkdirIfNotExist(folder); err != nil {
		t.Fatalf("failed to create folder: %v", err)
	}
	for i := range parts {
		// write half of the part's size
		start := parts[i].RangeFrom
		end := parts[i].RangeTo
		if end == int64(len(content)) { // last part uses open end, approximate size
			end = int64(len(content) - 1)
		}
		partSize := end - start + 1
		writeSize := partSize / 2
		if writeSize <= 0 {
			writeSize = 1
		}
		slice := content[start : start+writeSize]
		if err := os.WriteFile(parts[i].Path, slice, 0600); err != nil {
			t.Fatalf("failed to write partial part: %v", err)
		}
	}

	state := &State{URL: url, Parts: parts}
	if err := state.Save(); err != nil {
		t.Fatalf("failed to save state: %v", err)
	}

	// Resume will bump RangeFrom by the existing file sizes
	resumed, err := Resume(url)
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}

	Execute(url, resumed, 4, false, "", "")

	out := TaskFromURL(url)
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("failed to read output: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("resumed content mismatch")
	}
}

func TestE2ERangeSupportedWithoutAcceptRanges(t *testing.T) {
	displayProgress = false
	restoreCwd := withTempCwd(t)
	defer restoreCwd()
	restoreDF := withTestDataFolder(t)
	defer restoreDF()

	content := makeContent(128 * 1024)
	path := "/noar.bin"
	ts, rangeCount := startTestServer(t, content, true, false, true, true, path)
	url := ts.URL + path

	Execute(url, nil, 3, false, "", "")

	// Confirm at least one ranged request happened
	if atomic.LoadInt32(rangeCount) == 0 {
		t.Fatalf("expected ranged requests when Accept-Ranges absent")
	}
	// Validate file
	out := TaskFromURL(url)
	got, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("download mismatch or error: %v", err)
	}
}

func TestE2EUnknownLengthSinglePart(t *testing.T) {
	displayProgress = false
	restoreCwd := withTempCwd(t)
	defer restoreCwd()
	restoreDF := withTestDataFolder(t)
	defer restoreDF()

	content := makeContent(64 * 1024)
	path := "/chunked.bin"
	// no Accept-Ranges, no Content-Length on HEAD or GET
	ts, _ := startTestServer(t, content, false, false, false, false, path)
	url := ts.URL + path

	Execute(url, nil, 4, false, "", "")

	out := TaskFromURL(url)
	got, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("download mismatch or error: %v", err)
	}
}

func TestE2EGlobalRateLimit(t *testing.T) {
	displayProgress = false
	restoreCwd := withTempCwd(t)
	defer restoreCwd()
	restoreDF := withTestDataFolder(t)
	defer restoreDF()

	content := makeContent(200 * 1024) // 200KB
	path := "/rate.bin"
	ts, _ := startTestServer(t, content, true, true, true, true, path)
	url := ts.URL + path

	start := time.Now()
	Execute(url, nil, 4, false, "", "100KB")
	dur := time.Since(start)

	// With global 100KB/s limit and a 100KB burst, 200KB typically completes ~1.0–1.1s.
	// Assert only that we are not effectively unthrottled (<0.9s would be suspicious on CI).
	if dur < 900*time.Millisecond {
		t.Fatalf("rate limiting too fast: %v", dur)
	}

	out := TaskFromURL(url)
	got, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("download mismatch or error: %v", err)
	}
}

func TestE2EInterruptCancelsAndSavesState(t *testing.T) {
	displayProgress = false
	restoreCwd := withTempCwd(t)
	defer restoreCwd()
	restoreDF := withTestDataFolder(t)
	defer restoreDF()

	content := makeContent(5 * 1024 * 1024) // 5MB
	path := "/sigint.bin"
	ts, _ := startTestServer(t, content, true, true, true, true, path)
	url := ts.URL + path

	// Send SIGINT shortly after starting the download to simulate Ctrl+C.
	doneSig := make(chan struct{})
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
		close(doneSig)
	}()

	start := time.Now()
	Execute(url, nil, 4, false, "", "50KB")
	<-doneSig
	dur := time.Since(start)

	// We expect Execute to return promptly after the interrupt.
	if dur > 10*time.Second {
		t.Fatalf("interrupt handling too slow: %v", dur)
	}

	// State should be saved, and final joined file should not exist.
	usr, _ := user.Current()
	folder := filepath.Join(usr.HomeDir, dataFolder, TaskFromURL(url))
	statePath := filepath.Join(folder, stateFileName)
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("expected state saved at %s, got err=%v", statePath, err)
	}
	if _, err := os.Stat(TaskFromURL(url)); err == nil {
		t.Fatalf("unexpected final file created despite interrupt")
	}

	// At least one part file should exist in the folder.
	entries, err := os.ReadDir(folder)
	if err != nil {
		t.Fatalf("failed to read folder: %v", err)
	}
	foundPart := false
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, TaskFromURL(url)+".part") {
			foundPart = true
			break
		}
	}
	if !foundPart {
		t.Fatalf("expected part files in %s after interrupt", folder)
	}
}
