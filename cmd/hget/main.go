package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"

	"github.com/abzcoding/hget/internal/downloader"
	"github.com/abzcoding/hget/internal/joiner"
	"github.com/abzcoding/hget/internal/state"
	"github.com/abzcoding/hget/internal/ui"
	"github.com/abzcoding/hget/internal/util"
)

// GitCommit is injected at build time via -ldflags.
var GitCommit string

func main() {
	var proxy, filePath, bwLimit, resumeTask string

	conn := flag.Int("n", runtime.NumCPU(), "number of connections")
	skiptls := flag.Bool("skip-tls", false, "skip certificate verification for https")
	verify := flag.Bool("verify", false, "download and verify the .sig GPG signature after download")
	flag.StringVar(&proxy, "proxy", "", "proxy for downloading, e.g. -proxy '127.0.0.1:12345' for socks5 or -proxy 'http://proxy.com:8080' for http proxy")
	flag.StringVar(&filePath, "file", "", "path to a file that contains one URL per line")
	flag.StringVar(&bwLimit, "rate", "", "bandwidth limit during download, e.g. -rate 10kB or -rate 10MiB")
	flag.StringVar(&resumeTask, "resume", "", "resume download task with given task name (or URL)")
	probe := flag.String("probe", "", "probe URL for range and content-length without downloading")
	timeout := flag.Duration("timeout", 15*time.Second, "timeout for awaiting response headers (e.g., 30s, 1m)")

	flag.Parse()
	args := flag.Args()

	// Probe diagnostics mode
	if *probe != "" {
		downloader.DebugProbe(*probe, *skiptls, proxy, *timeout)
		return
	}

	// If the resume flag is provided, use that path (ignoring other arguments)
	if resumeTask != "" {
		st, err := state.Resume(resumeTask)
		if err != nil {
			if !os.IsNotExist(err) {
				ui.Errorf("Resume failed: %v\n", err)
				os.Exit(1)
			}
			// No state.json — try to reconstruct from existing part files.
			st, err = downloader.ReconstructStateFromParts(resumeTask, *skiptls, proxy, *timeout)
			if err == nil {
				ui.Printf("Reconstructed state from %d part files — resuming.\n", len(st.Parts))
				runWithTUI(func() {
					Execute(st.URL, st, *conn, *skiptls, proxy, bwLimit, *timeout)
				}, *conn, false, 0, 0)
				return
			}
			// No part files either — start fresh if it looks like a URL.
			ui.Warnf("No saved state found for %q — starting fresh download.\n", resumeTask)
			if !util.IsURL(resumeTask) {
				ui.Errorf("No saved state found for task %q and it is not a URL.\n", resumeTask)
				os.Exit(1)
			}
			runWithTUI(func() {
				Execute(resumeTask, nil, *conn, *skiptls, proxy, bwLimit, *timeout)
			}, *conn, false, 0, 0)
			return
		}
		runWithTUI(func() {
			Execute(st.URL, st, *conn, *skiptls, proxy, bwLimit, *timeout)
		}, *conn, false, 0, 0)
		return
	}

	// If no resume flag, then check for positional URL or file input
	if len(args) < 1 {
		if len(filePath) < 1 {
			ui.Errorln("A URL or input file with URLs is required")
			usage()
			os.Exit(1)
		}
		runBatchDownloads(filePath, *conn, *skiptls, proxy, bwLimit, *timeout, *verify)
		return
	}

	// Otherwise, if a URL is provided as positional argument, treat it as a new download.
	downloadURL := args[0]

	// ── File-already-exists check ─────────────────────────────────────────────
	destFile := util.TaskFromURL(downloadURL)
	if _, err := os.Stat(destFile); err == nil {
		// File exists — ask the user only when running interactively.
		if isatty.IsTerminal(os.Stdout.Fd()) {
			proceed := confirmRedownload(destFile)
			if !proceed {
				ui.Warnf("Skipping download — %s already exists.\n", destFile)
				if *verify {
					ok, detail := runVerify(downloadURL, *skiptls, proxy, *timeout)
					printVerifySummary(ok, detail)
				}
				return
			}
		}
	}

	// Check if a folder already exists for the task and remove if necessary.
	if util.ExistDir(state.FolderOf(downloadURL)) {
		ui.Warnf("Downloading task already exists, remove it first\n")
		err := os.RemoveAll(state.FolderOf(downloadURL))
		util.FatalCheck(err)
	}
	var verifyOK bool
	var verifyDetail string
	var didVerify bool

	runWithTUI(func() {
		Execute(downloadURL, nil, *conn, *skiptls, proxy, bwLimit, *timeout)
		if *verify {
			verifyOK, verifyDetail = runVerify(downloadURL, *skiptls, proxy, *timeout)
			didVerify = true
		}
	}, *conn, *verify, 0, 0)

	// After the TUI alt-screen closes, print the verify result to the normal terminal.
	if didVerify {
		printVerifySummary(verifyOK, verifyDetail)
	}
}

// runWithTUI starts a Bubble Tea program for interactive TTY sessions and runs fn
// in a background goroutine. Falls back to plain execution when not in a TTY.
// willVerify informs the TUI model so it can show the verification phase.
// batchCurrent/batchTotal are 1-based; pass 0,0 when not in batch mode.
func runWithTUI(fn func(), numConns int, willVerify bool, batchCurrent, batchTotal int) {
	if isatty.IsTerminal(os.Stdout.Fd()) && ui.DisplayProgress {
		model := ui.NewTUIModel(numConns, willVerify, batchCurrent, batchTotal)
		p := tea.NewProgram(model, tea.WithAltScreen())
		ui.Program = p
		go func() {
			var downloadErr error
			defer func() {
				if r := recover(); r != nil {
					if err, ok := r.(error); ok {
						downloadErr = err
					} else {
						downloadErr = fmt.Errorf("%v", r)
					}
				}
				if downloadErr != nil {
					p.Send(ui.DownloadErrorMsg{Err: downloadErr})
				} else {
					p.Send(ui.DownloadDoneMsg{})
				}
			}()
			fn()
		}()
		if _, err := p.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "TUI error:", err)
			os.Exit(1)
		}
		// Clear the program handle so post-TUI log calls go to charmbracelet/log.
		ui.Program = nil
		return
	}
	// Non-TTY: run directly.
	fn()
}

// Execute configures the HTTPDownloader and uses it to download the target.
func Execute(url string, st *state.State, conn int, skiptls bool, proxyServer string, bwLimit string, timeout time.Duration) {
	// Capture OS interrupt signals
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)
	defer signal.Stop(signalChan)

	var isInterrupted = false

	var dl *downloader.HTTPDownloader
	if st == nil {
		dl = downloader.NewHTTPDownloader(url, conn, skiptls, proxyServer, bwLimit, timeout)
	} else {
		// Rebuild client with current connection settings so proxy/TLS/timeout are honoured.
		client := downloader.ProxyAwareHTTPClient(proxyServer, skiptls, timeout)
		dl = downloader.NewHTTPDownloaderFromState(st, client, proxyServer, skiptls, timeout)
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

// runBatchDownloads reads URLs from filePath and downloads them one by one,
// printing a live queue panel (via lipgloss) before each download and a final
// summary table afterwards.
func runBatchDownloads(filePath string, conn int, skiptls bool, proxy, bwLimit string, timeout time.Duration, verify bool) {
	// ── Read and validate URL list ────────────────────────────────────────────
	f, err := os.Open(filePath)
	util.FatalCheck(err)
	defer f.Close()

	var urls []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		urls = append(urls, line)
	}
	util.FatalCheck(scanner.Err())

	if len(urls) == 0 {
		ui.Warnf("No URLs found in %s\n", filePath)
		return
	}

	// ── Palette & styles (mirrors the TUI palette) ────────────────────────────
	cPurple := lipgloss.Color("#C77DFF")
	cCyan := lipgloss.Color("#00B4D8")
	cGreen := lipgloss.Color("#06D6A0")
	cYellow := lipgloss.Color("#FFB703")
	cRed := lipgloss.Color("#EF233C")
	cMuted := lipgloss.Color("#6C757D")
	cBorder := lipgloss.Color("#495057")

	styleSep := lipgloss.NewStyle().Foreground(cBorder)
	styleCounter := lipgloss.NewStyle().Foreground(cPurple).Bold(true)
	styleFile := lipgloss.NewStyle().Foreground(cCyan)
	styleURL := lipgloss.NewStyle().Foreground(cMuted)
	styleDone := lipgloss.NewStyle().Foreground(cGreen).Bold(true)
	styleFail := lipgloss.NewStyle().Foreground(cRed).Bold(true)
	styleSkip := lipgloss.NewStyle().Foreground(cYellow)
	stylePending := lipgloss.NewStyle().Foreground(cMuted)
	styleActive := lipgloss.NewStyle().Foreground(cCyan).Bold(true)
	styleMuted := lipgloss.NewStyle().Foreground(cMuted)
	styleBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cPurple).
		Padding(0, 2).
		Foreground(cCyan)

	const sepW = 68
	sep := styleSep.Render(strings.Repeat("─", sepW))

	// ── Item status tracking ──────────────────────────────────────────────────
	type itemStatus int
	const (
		statusPending itemStatus = iota
		statusActive
		statusDone
		statusFailed
		statusSkipped
	)

	type item struct {
		url    string
		file   string
		status itemStatus
		reason string
	}

	items := make([]item, len(urls))
	for i, u := range urls {
		items[i] = item{url: u, file: util.TaskFromURL(u), status: statusPending}
	}

	// printQueuePanel renders the current queue state to stdout.
	printQueuePanel := func(activeIdx int) {
		done, failed, skipped := 0, 0, 0
		for _, it := range items {
			switch it.status {
			case statusDone:
				done++
			case statusFailed:
				failed++
			case statusSkipped:
				skipped++
			}
		}
		remaining := len(items) - done - failed - skipped
		if activeIdx >= 0 {
			remaining-- // the active one is not "remaining"
		}

		// Header box
		hdr := fmt.Sprintf("  Batch  ·  %d file", len(items))
		if len(items) != 1 {
			hdr += "s"
		}
		if verify {
			hdr += "  ·  verify on"
		}
		if done+failed+skipped > 0 {
			hdr += fmt.Sprintf("  ·  %d done", done+skipped)
			if failed > 0 {
				hdr += fmt.Sprintf("  ·  %d failed", failed)
			}
		}
		if remaining > 0 {
			hdr += fmt.Sprintf("  ·  %d left", remaining)
		}
		fmt.Println()
		fmt.Println(styleBox.Render(hdr))
		fmt.Println()

		// Item list
		for i, it := range items {
			var icon, nameStr, statusStr string
			fname := it.file
			if len(fname) > 40 {
				fname = fname[:39] + "…"
			}
			switch it.status {
			case statusDone:
				icon = styleDone.Render("  ✓")
				nameStr = styleDone.Render(fname)
				statusStr = styleDone.Render("done")
			case statusFailed:
				icon = styleFail.Render("  ✗")
				nameStr = styleFail.Render(fname)
				statusStr = styleFail.Render("failed")
				if it.reason != "" {
					statusStr += "  " + styleMuted.Render("("+truncateSummary(it.reason, 35)+")")
				}
			case statusSkipped:
				icon = styleSkip.Render("  ─")
				nameStr = styleSkip.Render(fname)
				statusStr = styleSkip.Render("skipped (exists)")
			case statusActive:
				icon = styleActive.Render("  ⬇")
				nameStr = styleActive.Render(fname)
				statusStr = styleActive.Render(fmt.Sprintf("downloading  [%d/%d]", i+1, len(items)))
			default:
				icon = stylePending.Render("  ◦")
				nameStr = stylePending.Render(fname)
				statusStr = stylePending.Render("pending")
			}
			// Pad filename to fixed width for alignment
			padded := nameStr + strings.Repeat(" ", max(0, 42-len(it.file)))
			fmt.Printf("%s  %s  %s\n", icon, padded, statusStr)
		}
		fmt.Println()
		fmt.Println(sep)
	}

	// ── Per-URL download loop ─────────────────────────────────────────────────
	for i := range items {
		it := &items[i]
		it.status = statusActive
		printQueuePanel(i)

		var itemOK = true
		var itemReason string

		// Show which file we're about to download
		fmt.Printf("\n  %s  %s\n",
			styleCounter.Render(fmt.Sprintf("[%d/%d]", i+1, len(items))),
			styleFile.Render(it.file),
		)
		fmt.Println(styleURL.Render("  " + it.url))
		fmt.Println()

		// File-exists check
		if _, statErr := os.Stat(it.file); statErr == nil {
			if isatty.IsTerminal(os.Stdout.Fd()) {
				proceed := confirmRedownload(it.file)
				if !proceed {
					ui.Warnf("Skipping — %s already exists.\n", it.file)
					if verify {
						ok, detail := runVerify(it.url, skiptls, proxy, timeout)
						printVerifySummary(ok, detail)
						if !ok {
							itemOK = false
							itemReason = detail
						}
					}
					if itemOK {
						it.status = statusSkipped
					} else {
						it.status = statusFailed
						it.reason = itemReason
					}
					fmt.Println()
					continue
				}
			}
		}

		// Remove stale temp dir
		if util.ExistDir(state.FolderOf(it.url)) {
			if rmErr := os.RemoveAll(state.FolderOf(it.url)); rmErr != nil {
				ui.Warnf("Could not remove old temp dir: %v\n", rmErr)
			}
		}

		var verifyOK bool
		var verifyDetail string
		var didVerify bool

		func() {
			defer func() {
				if r := recover(); r != nil {
					itemOK = false
					if e, ok := r.(error); ok {
						itemReason = e.Error()
					} else {
						itemReason = fmt.Sprintf("%v", r)
					}
				}
			}()
			runWithTUI(func() {
				Execute(it.url, nil, conn, skiptls, proxy, bwLimit, timeout)
				if verify {
					verifyOK, verifyDetail = runVerify(it.url, skiptls, proxy, timeout)
					didVerify = true
				}
			}, conn, verify, i+1, len(items))
		}()

		if didVerify {
			printVerifySummary(verifyOK, verifyDetail)
			if !verifyOK && itemOK {
				itemOK = false
				itemReason = verifyDetail
			}
		}

		if itemOK {
			it.status = statusDone
		} else {
			it.status = statusFailed
			it.reason = itemReason
		}
		fmt.Println()
	}

	// ── Final summary panel ───────────────────────────────────────────────────
	printQueuePanel(-1)

	done, failed, skipped := 0, 0, 0
	for _, it := range items {
		switch it.status {
		case statusDone, statusSkipped:
			done++
		case statusFailed:
			failed++
		}
		_ = skipped
	}
	if failed == 0 {
		fmt.Println(styleDone.Render(fmt.Sprintf("  ✓  All %d downloads complete.", len(items))))
	} else {
		fmt.Println(styleFail.Render(fmt.Sprintf("  ✗  %d/%d failed.", failed, len(items))))
	}
	fmt.Println()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func truncateSummary(s string, max int) string {
	// Take only the first line and cap length.
	if idx := strings.IndexByte(s, '\n'); idx != -1 {
		s = s[:idx]
	}
	s = strings.TrimSpace(s)
	if len(s) > max {
		return s[:max-1] + "…"
	}
	return s
}

// confirmRedownload shows a styled huh confirmation prompt asking whether to
// overwrite an existing file.  Returns true when the user says yes.
func confirmRedownload(filename string) bool {
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#C77DFF")).Bold(true)
	fileStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#00B4D8"))

	fmt.Println()
	fmt.Println(labelStyle.Render("  ⚠  File already exists:") + " " + fileStyle.Render(filename))
	fmt.Println()

	var proceed bool
	f := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Download again and overwrite?").
				Value(&proceed).
				Affirmative("Yes, overwrite").
				Negative("No, keep existing"),
		),
	)
	// Run with default theme; ignore error (treated as "no").
	_ = f.Run()
	return proceed
}

// runVerify downloads the .sig file for url and runs gpg --verify.
// It sends TUI messages when ui.Program is active (during the TUI alt-screen).
// It always returns (ok, detail) so the caller can print a post-TUI summary.
func runVerify(url string, skipTLS bool, proxyServer string, timeout time.Duration) (ok bool, detail string) {
	sigURL := url + ".sig"
	destFile := util.TaskFromURL(url)
	sigFile := destFile + ".sig"

	// Announce start — goes to TUI log panel or charmbracelet/log.
	if ui.Program != nil {
		ui.Program.Send(ui.VerifyStartMsg{})
	} else {
		ui.Printf("Fetching signature from %s\n", sigURL)
	}

	if err := downloader.DownloadSigFile(sigURL, sigFile, skipTLS, proxyServer, timeout); err != nil {
		msg := fmt.Sprintf("could not download .sig file: %v", err)
		if ui.Program != nil {
			ui.Program.Send(ui.VerifyDoneMsg{OK: false, Detail: msg})
		}
		return false, msg
	}
	defer os.Remove(sigFile) //nolint:errcheck

	gpgDetail, err := downloader.VerifyGPGSignature(sigFile, destFile)
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

// printVerifySummary writes a styled one-line verify result to the terminal
// using charmbracelet/log (works after the TUI alt-screen has closed).
func printVerifySummary(ok bool, detail string) {
	if ok {
		ui.Printf("Signature valid — %s\n", detail)
	} else {
		ui.Errorf("Signature invalid — %s\n", detail)
	}
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

func usage() {
	ui.Printf(`Usage:
 hget [options] URL
 hget [options] --resume=TaskName

 Options:
   -n int          number of connections (default number of CPUs)
   -skip-tls bool  skip certificate verification for https (default false)
   -proxy string   proxy address (e.g., '127.0.0.1:12345' for socks5 or 'http://proxy.com:8080')
   -file string    file path containing URLs (one per line)
   -rate string    bandwidth limit during download (e.g., 10kB, 10MiB)
   -resume string  resume a stopped download by providing its task name or URL
   -probe string   probe URL for range and content-length without downloading
   -timeout        timeout for awaiting response headers (default 15s)
   --verify        download and GPG-verify the .sig signature file after download
`)
}
