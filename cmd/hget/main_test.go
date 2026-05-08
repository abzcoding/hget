package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abzcoding/hget/internal/downloader"
	"github.com/abzcoding/hget/internal/state"
	"github.com/abzcoding/hget/internal/ui"
	"github.com/abzcoding/hget/internal/util"
)

// Execute is a backward-compat wrapper around downloader.Execute that uses a
// background context, mirroring the pre-refactor signature used by tests.
func Execute(url string, st *state.State, conn int, skiptls bool, proxy, bwLimit string, timeout time.Duration) {
	_ = downloader.Execute(context.Background(), url, st, conn, skiptls, proxy, bwLimit, timeout)
}

func makeContent(size int) []byte {
	data := make([]byte, size)
	for i := 0; i < size; i++ {
		data[i] = byte('A' + (i % 23))
	}
	return data
}

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
			rangeHeader := r.Header.Get("Range")
			if supportRange && rangeHeader != "" && strings.HasPrefix(rangeHeader, "bytes=") {
				atomic.AddInt32(&rangeRequests, 1)
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
	original := state.DataFolder
	state.DataFolder = ".hget_e2e_test/"
	return func() {
		state.DataFolder = original
		usr, _ := user.Current()
		_ = os.RemoveAll(filepath.Join(usr.HomeDir, state.DataFolder))
	}
}

func TestE2EParallelDownloadWithRange(t *testing.T) {
	ui.DisplayProgress = false
	restoreCwd := withTempCwd(t)
	defer restoreCwd()
	restoreDF := withTestDataFolder(t)
	defer restoreDF()

	content := makeContent(256 * 1024)
	path := "/file.bin"
	ts, _ := startTestServer(t, content, true, true, true, true, path)

	url := ts.URL + path
	Execute(url, nil, 4, false, "", "", 15*time.Second)

	out := util.TaskFromURL(url)
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("failed to read output: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("downloaded content mismatch")
	}

	usr, _ := user.Current()
	folder := filepath.Join(usr.HomeDir, state.DataFolder, util.TaskFromURL(url))
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Fatalf("expected data folder removed, got err=%v", err)
	}
}

func TestE2EResumeDownload(t *testing.T) {
	ui.DisplayProgress = false
	restoreCwd := withTempCwd(t)
	defer restoreCwd()
	restoreDF := withTestDataFolder(t)
	defer restoreDF()

	content := makeContent(300 * 1024)
	path := "/resume.bin"
	ts, _ := startTestServer(t, content, true, true, true, true, path)
	url := ts.URL + path

	parts, err := downloader.PartCalculate(4, int64(len(content)), url)
	if err != nil {
		t.Fatalf("PartCalculate failed: %v", err)
	}
	folder := state.FolderOf(url)
	if err := util.MkdirIfNotExist(folder); err != nil {
		t.Fatalf("failed to create folder: %v", err)
	}
	for i := range parts {
		start := parts[i].RangeFrom
		end := parts[i].RangeTo
		if end == int64(len(content)) {
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
		parts[i].RangeFrom = start + writeSize
	}

	st := &state.State{URL: url, Parts: parts}
	if err := st.Save(); err != nil {
		t.Fatalf("failed to save state: %v", err)
	}

	resumed, err := state.Resume(url)
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}

	Execute(url, resumed, 4, false, "", "", 15*time.Second)

	out := util.TaskFromURL(url)
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("failed to read output: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("resumed content mismatch")
	}
}

func TestE2ERangeSupportedWithoutAcceptRanges(t *testing.T) {
	ui.DisplayProgress = false
	restoreCwd := withTempCwd(t)
	defer restoreCwd()
	restoreDF := withTestDataFolder(t)
	defer restoreDF()

	content := makeContent(128 * 1024)
	path := "/noar.bin"
	ts, rangeCount := startTestServer(t, content, true, false, true, true, path)
	url := ts.URL + path

	Execute(url, nil, 3, false, "", "", 15*time.Second)

	if atomic.LoadInt32(rangeCount) == 0 {
		t.Fatalf("expected ranged requests when Accept-Ranges absent")
	}
	out := util.TaskFromURL(url)
	got, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("download mismatch or error: %v", err)
	}
}

func TestE2EUnknownLengthSinglePart(t *testing.T) {
	ui.DisplayProgress = false
	restoreCwd := withTempCwd(t)
	defer restoreCwd()
	restoreDF := withTestDataFolder(t)
	defer restoreDF()

	content := makeContent(64 * 1024)
	path := "/chunked.bin"
	ts, _ := startTestServer(t, content, false, false, false, false, path)
	url := ts.URL + path

	Execute(url, nil, 4, false, "", "", 15*time.Second)

	out := util.TaskFromURL(url)
	got, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("download mismatch or error: %v", err)
	}
}

func TestE2EGlobalRateLimit(t *testing.T) {
	ui.DisplayProgress = false
	restoreCwd := withTempCwd(t)
	defer restoreCwd()
	restoreDF := withTestDataFolder(t)
	defer restoreDF()

	content := makeContent(200 * 1024) // 200KB
	path := "/rate.bin"
	ts, _ := startTestServer(t, content, true, true, true, true, path)
	url := ts.URL + path

	start := time.Now()
	Execute(url, nil, 4, false, "", "100KB", 15*time.Second)
	dur := time.Since(start)

	if dur < 900*time.Millisecond {
		t.Fatalf("rate limiting too fast: %v", dur)
	}

	out := util.TaskFromURL(url)
	got, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("download mismatch or error: %v", err)
	}
}

func TestE2EInterruptCancelsAndSavesState(t *testing.T) {
	ui.DisplayProgress = false
	restoreCwd := withTempCwd(t)
	defer restoreCwd()
	restoreDF := withTestDataFolder(t)
	defer restoreDF()

	content := makeContent(5 * 1024 * 1024) // 5MB
	path := "/sigint.bin"
	ts, _ := startTestServer(t, content, true, true, true, true, path)
	url := ts.URL + path

	ctx, cancel := context.WithCancelCause(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel(downloader.ErrUserQuit)
	}()

	start := time.Now()
	err := downloader.Execute(ctx, url, nil, 4, false, "", "50KB", 15*time.Second)
	dur := time.Since(start)

	if dur > 10*time.Second {
		t.Fatalf("interrupt handling too slow: %v", dur)
	}
	if !errors.Is(err, downloader.ErrUserQuit) && !errors.Is(err, context.Canceled) {
		t.Fatalf("expected ErrUserQuit/Canceled, got: %v", err)
	}

	usr, _ := user.Current()
	folder := filepath.Join(usr.HomeDir, state.DataFolder, util.TaskFromURL(url))
	statePath := filepath.Join(folder, state.StateFileName)
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("expected state saved at %s, got err=%v", statePath, err)
	}
	if _, err := os.Stat(util.TaskFromURL(url)); err == nil {
		t.Fatalf("unexpected final file created despite interrupt")
	}

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
		if strings.HasPrefix(name, util.TaskFromURL(url)+".part") {
			foundPart = true
			break
		}
	}
	if !foundPart {
		t.Fatalf("expected part files in %s after interrupt", folder)
	}
}

// TestE2ESkipDiscardsState verifies that cancelling Execute with
// ErrSkipCurrent removes the partial download folder instead of saving state.
func TestE2ESkipDiscardsState(t *testing.T) {
	ui.DisplayProgress = false
	restoreCwd := withTempCwd(t)
	defer restoreCwd()
	restoreDF := withTestDataFolder(t)
	defer restoreDF()

	content := makeContent(2 * 1024 * 1024)
	path := "/skip.bin"
	ts, _ := startTestServer(t, content, true, true, true, true, path)
	url := ts.URL + path

	ctx, cancel := context.WithCancelCause(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel(downloader.ErrSkipCurrent)
	}()

	err := downloader.Execute(ctx, url, nil, 4, false, "", "50KB", 15*time.Second)
	if !errors.Is(err, downloader.ErrSkipCurrent) {
		t.Fatalf("expected ErrSkipCurrent, got: %v", err)
	}

	usr, _ := user.Current()
	folder := filepath.Join(usr.HomeDir, state.DataFolder, util.TaskFromURL(url))
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Fatalf("expected folder %s removed after skip, stat err=%v", folder, err)
	}
}
