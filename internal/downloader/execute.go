package downloader

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/abzcoding/hget/internal/joiner"
	"github.com/abzcoding/hget/internal/state"
	"github.com/abzcoding/hget/internal/ui"
	"github.com/abzcoding/hget/internal/util"
)

// Cancellation causes used as the second argument to context.CancelCauseFunc.
// Execute and the batch loop use these to distinguish user intent.
var (
	// ErrSkipCurrent — user requested to skip the current download
	// (state is discarded, batch continues).
	ErrSkipCurrent = errors.New("skip current item")

	// ErrAbortBatch — user wants the entire batch to stop immediately
	// (state is saved when resumable, no further items are started).
	ErrAbortBatch = errors.New("abort batch")

	// ErrUserQuit — single-download equivalent of ErrAbortBatch.
	// State is saved when resumable.
	ErrUserQuit = errors.New("user quit")
)

// Execute downloads url, observing ctx for cancellation.
// Returns:
//   - nil on success;
//   - context.Cause(ctx) (one of ErrSkipCurrent, ErrAbortBatch, ErrUserQuit,
//     or context.Canceled) when ctx was cancelled;
//   - the underlying download/IO error on failure.
//
// Execute always waits for every part goroutine to exit before returning,
// guaranteeing that no orphaned goroutines from the previous run can write
// into a future TUI session's ui.Program handle.
func Execute(ctx context.Context, url string, st *state.State, conn int, skiptls bool, proxyServer string, bwLimit string, timeout time.Duration) error {
	var dl *HTTPDownloader
	if st == nil {
		dl = NewHTTPDownloader(url, conn, skiptls, proxyServer, bwLimit, timeout)
	} else {
		client := ProxyAwareHTTPClient(proxyServer, skiptls, timeout)
		dl = NewHTTPDownloaderFromState(st, client, proxyServer, skiptls, timeout)
		if ui.Program != nil {
			ui.Program.Send(ui.DownloadStartMsg{
				URL:      st.URL,
				FileName: util.TaskFromURL(st.URL),
				NumParts: len(st.Parts),
			})
		}
	}

	numParts := dl.NumParts()

	doneChan := make(chan bool, 1)
	fileChan := make(chan string, numParts)
	errorChan := make(chan error, numParts)
	stateChan := make(chan state.Part, numParts)
	interruptChan := make(chan bool, numParts)

	go dl.Do(doneChan, fileChan, errorChan, interruptChan, stateChan)

	var parts []state.Part
	var files []string
	var firstErr error
	interrupted := false

	broadcastInterrupt := func() {
		if interrupted {
			return
		}
		interrupted = true
		for i := 0; i < numParts; i++ {
			select {
			case interruptChan <- true:
			default:
			}
		}
	}

loop:
	for {
		select {
		case <-ctx.Done():
			broadcastInterrupt()
		case file := <-fileChan:
			files = append(files, file)
		case err := <-errorChan:
			if firstErr == nil {
				firstErr = err
			}
			// Cancel siblings; keep draining until they all report back so we
			// never leak goroutines into the next TUI session.
			broadcastInterrupt()
		case part := <-stateChan:
			parts = append(parts, part)
		case <-doneChan:
			// Drain remaining in-flight notifications from any goroutines
			// that finished after doneChan was signalled.
			for len(files) < numParts {
				select {
				case f := <-fileChan:
					files = append(files, f)
				default:
					files = append(files, "")
				}
			}
			for len(parts) < numParts {
				select {
				case p := <-stateChan:
					parts = append(parts, p)
				default:
					parts = append(parts, state.Part{})
				}
			}
			break loop
		}
	}

	if interrupted {
		cause := context.Cause(ctx)
		if cause == nil && firstErr != nil {
			cause = firstErr
		}
		folder := state.FolderOf(url)

		switch {
		case errors.Is(cause, ErrSkipCurrent):
			// User asked to skip — discard partial state; batch will move on.
			ui.Warnf("Skipped — discarding partial download for %s\n", util.TaskFromURL(url))
			_ = os.RemoveAll(folder)
		case dl.IsResumable():
			ui.Printf("Interrupted — saving state…\n")
			s := &state.State{URL: url, Parts: parts}
			if err := s.Save(); err != nil {
				ui.Errorf("Save failed: %v\n", err)
			}
		default:
			ui.Warnf("Interrupted, download is not resumable.\n")
		}

		if firstErr != nil && cause == firstErr {
			if ui.Program != nil {
				ui.Program.Send(ui.DownloadErrorMsg{Err: firstErr})
			}
			return firstErr
		}
		if cause != nil {
			return cause
		}
		return context.Canceled
	}

	if firstErr != nil {
		ui.Errorf("%v\n", firstErr)
		if ui.Program != nil {
			ui.Program.Send(ui.DownloadErrorMsg{Err: firstErr})
		}
		return firstErr
	}

	// Collect part files from the temp folder (source of truth, avoids channel races).
	folder := state.FolderOf(url)
	entries, readErr := os.ReadDir(folder)
	if readErr != nil {
		err := fmt.Errorf("read parts directory: %w", readErr)
		if ui.Program != nil {
			ui.Program.Send(ui.DownloadErrorMsg{Err: err})
		}
		return err
	}
	files = files[:0]
	prefix := util.TaskFromURL(url) + ".part"
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			files = append(files, folder+string(os.PathSeparator)+e.Name())
		}
	}

	if err := joiner.JoinFile(files, util.TaskFromURL(url)); err != nil {
		ui.Errorf("Join failed: %v\n", err)
		if ui.Program != nil {
			ui.Program.Send(ui.DownloadErrorMsg{Err: err})
		}
		return err
	}
	if err := os.RemoveAll(state.FolderOf(url)); err != nil {
		ui.Warnf("Cleanup failed: %v\n", err)
	}
	return nil
}

// RunVerify downloads the .sig file for url and runs gpg --verify.
// It sends TUI messages when ui.Program is active (during the TUI alt-screen).
// It always returns (ok, detail) so the caller can print a post-TUI summary.
func RunVerify(url string, skipTLS bool, proxyServer string, timeout time.Duration) (ok bool, detail string) {
	sigURL := buildSigURL(url)
	destFile := util.TaskFromURL(url)
	sigFile := destFile + ".sig"

	if ui.Program != nil {
		ui.Program.Send(ui.VerifyStartMsg{})
	} else {
		ui.Printf("Fetching signature from %s\n", sigURL)
	}

	if err := DownloadSigFile(sigURL, sigFile, skipTLS, proxyServer, timeout); err != nil {
		msg := fmt.Sprintf("could not download .sig file: %v", err)
		if ui.Program != nil {
			ui.Program.Send(ui.VerifyDoneMsg{OK: false, Detail: msg})
		}
		return false, msg
	}
	defer os.Remove(sigFile) //nolint:errcheck

	gpgDetail, err := VerifyGPGSignature(sigFile, destFile)
	if err != nil {
		result := strings.TrimSpace(fmt.Sprintf("%v", err))
		if ui.Program != nil {
			ui.Program.Send(ui.VerifyDoneMsg{OK: false, Detail: gpgDetail})
		}
		return false, result
	}
	summary := extractGPGSummary(gpgDetail)
	if ui.Program != nil {
		ui.Program.Send(ui.VerifyDoneMsg{OK: true, Detail: summary})
	}
	return true, summary
}

// extractGPGSummary returns the most relevant line from gpg's output (the
// "Good signature from …" line, or the last non-empty line as a fallback).
func extractGPGSummary(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, l := range lines {
		if strings.Contains(strings.ToLower(l), "good signature") {
			return strings.TrimSpace(l)
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return strings.TrimSpace(lines[i])
		}
	}
	return output
}
