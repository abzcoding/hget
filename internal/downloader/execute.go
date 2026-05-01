package downloader

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/abzcoding/hget/internal/joiner"
	"github.com/abzcoding/hget/internal/state"
	"github.com/abzcoding/hget/internal/ui"
	"github.com/abzcoding/hget/internal/util"
)

// Execute configures the HTTPDownloader and uses it to download the target.
func Execute(url string, st *state.State, conn int, skiptls bool, proxyServer string, bwLimit string, timeout time.Duration) {
	// Capture OS interrupt signals.
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)
	defer signal.Stop(signalChan)

	var isInterrupted = false

	var dl *HTTPDownloader
	if st == nil {
		dl = NewHTTPDownloader(url, conn, skiptls, proxyServer, bwLimit, timeout)
	} else {
		// Rebuild client with current connection settings so proxy/TLS/timeout are honoured.
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

loop:
	for {
		select {
		case <-signalChan:
			if !isInterrupted {
				isInterrupted = true
				for i := 0; i < numParts; i++ {
					interruptChan <- true
				}
			}
		case file := <-fileChan:
			files = append(files, file)
		case err := <-errorChan:
			ui.Errorf("%v\n", err)
			if ui.Program != nil {
				ui.Program.Send(ui.DownloadErrorMsg{Err: err})
			} else {
				os.Exit(1)
			}
			return
		case part := <-stateChan:
			parts = append(parts, part)
		case <-doneChan:
			// Drain remaining in-flight notifications.
			for len(files) < numParts {
				files = append(files, <-fileChan)
			}
			for len(parts) < numParts {
				parts = append(parts, <-stateChan)
			}
			break loop
		}
	}

	if isInterrupted {
		if dl.IsResumable() {
			ui.Printf("Interrupted — saving state…\n")
			s := &state.State{URL: url, Parts: parts}
			if err := s.Save(); err != nil {
				ui.Errorf("Save failed: %v\n", err)
			}
		} else {
			ui.Warnf("Interrupted, download is not resumable.\n")
		}
		return
	}

	// Collect part files from the temp folder (source of truth, avoids channel races).
	folder := state.FolderOf(url)
	entries, readErr := os.ReadDir(folder)
	util.FatalCheck(readErr)
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
		return
	}
	if err := os.RemoveAll(state.FolderOf(url)); err != nil {
		ui.Warnf("Cleanup failed: %v\n", err)
	}
}

// RunVerify downloads the .sig file for url and runs gpg --verify.
// It sends TUI messages when ui.Program is active (during the TUI alt-screen).
// It always returns (ok, detail) so the caller can print a post-TUI summary.
func RunVerify(url string, skipTLS bool, proxyServer string, timeout time.Duration) (ok bool, detail string) {
	sigURL := url + ".sig"
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
