package batch

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/abzcoding/hget/internal/downloader"
	"github.com/abzcoding/hget/internal/state"
	"github.com/abzcoding/hget/internal/ui"
	"github.com/abzcoding/hget/internal/util"
)

// RunBatchDownloads reads URLs from filePath and downloads them one by one,
// printing a live queue panel before each download and a final summary
// afterwards.  ctx is the *batch* cancellation context — when it is cancelled
// (typically by SIGINT routed through signal.NotifyContext, or by the user
// pressing 'q' in the TUI) the loop exits cleanly and remaining items are
// reported as "aborted".
func RunBatchDownloads(ctx context.Context, filePath string, conn int, skiptls bool, proxy, bwLimit string, timeout time.Duration, verify bool) {
	// ── Read and validate URL list ────────────────────────────────────────────
	f, err := os.Open(filePath)
	if err != nil {
		ui.Errorf("could not open URL list %s: %v\n", filePath, err)
		return
	}
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
	if scanErr := scanner.Err(); scanErr != nil {
		ui.Errorf("error reading %s: %v\n", filePath, scanErr)
		return
	}

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
	styleAbort := lipgloss.NewStyle().Foreground(cYellow).Bold(true)
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
		statusAborted
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
		done, failed, skipped, aborted := 0, 0, 0, 0
		for _, it := range items {
			switch it.status {
			case statusDone:
				done++
			case statusFailed:
				failed++
			case statusSkipped:
				skipped++
			case statusAborted:
				aborted++
			}
		}
		remaining := len(items) - done - failed - skipped - aborted
		if activeIdx >= 0 {
			remaining-- // the active one is not "remaining"
		}
		if remaining < 0 {
			remaining = 0
		}

		hdr := fmt.Sprintf("  Batch  ·  %d file", len(items))
		if len(items) != 1 {
			hdr += "s"
		}
		if verify {
			hdr += "  ·  verify on"
		}
		if done > 0 {
			hdr += fmt.Sprintf("  ·  %d done", done)
		}
		if skipped > 0 {
			hdr += fmt.Sprintf("  ·  %d skipped", skipped)
		}
		if failed > 0 {
			hdr += fmt.Sprintf("  ·  %d failed", failed)
		}
		if aborted > 0 {
			hdr += fmt.Sprintf("  ·  %d aborted", aborted)
		}
		if remaining > 0 {
			hdr += fmt.Sprintf("  ·  %d left", remaining)
		}
		fmt.Println()
		fmt.Println(styleBox.Render(hdr))
		fmt.Println()

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
				statusStr = styleSkip.Render("skipped")
				if it.reason != "" {
					statusStr += "  " + styleMuted.Render("("+truncateSummary(it.reason, 35)+")")
				}
			case statusAborted:
				icon = styleAbort.Render("  ⊘")
				nameStr = styleAbort.Render(fname)
				statusStr = styleAbort.Render("aborted")
			case statusActive:
				icon = styleActive.Render("  ⬇")
				nameStr = styleActive.Render(fname)
				statusStr = styleActive.Render(fmt.Sprintf("downloading  [%d/%d]", i+1, len(items)))
			default:
				icon = stylePending.Render("  ◦")
				nameStr = stylePending.Render(fname)
				statusStr = stylePending.Render("pending")
			}
			padded := nameStr + strings.Repeat(" ", max(0, 42-len(fname)))
			fmt.Printf("%s  %s  %s\n", icon, padded, statusStr)
		}
		fmt.Println()
		fmt.Println(sep)
	}

	// ── Per-URL download loop ─────────────────────────────────────────────────
	for i := range items {
		// Honour an external abort (SIGINT, q during a previous item, etc.)
		// before starting the next download.
		if ctx.Err() != nil {
			for j := i; j < len(items); j++ {
				if items[j].status == statusPending {
					items[j].status = statusAborted
				}
			}
			break
		}

		it := &items[i]
		it.status = statusActive
		printQueuePanel(i)

		var itemReason string

		fmt.Printf("\n  %s  %s\n",
			styleCounter.Render(fmt.Sprintf("[%d/%d]", i+1, len(items))),
			styleFile.Render(it.file),
		)
		fmt.Println(styleURL.Render("  " + it.url))
		fmt.Println()

		// File-exists check (isatty gate is inside ui.ConfirmRedownload).
		if _, statErr := os.Stat(it.file); statErr == nil {
			if !ui.ConfirmRedownload(it.file) {
				ui.Warnf("Skipping — %s already exists.\n", it.file)
				it.status = statusSkipped
				it.reason = "already exists"
				if verify {
					ok, detail := downloader.RunVerify(it.url, skiptls, proxy, timeout)
					ui.PrintVerifySummary(ok, detail)
					if !ok {
						it.status = statusFailed
						it.reason = detail
					}
				}
				fmt.Println()
				continue
			}
		}

		// Remove stale temp dir.
		if util.ExistDir(state.FolderOf(it.url)) {
			if rmErr := os.RemoveAll(state.FolderOf(it.url)); rmErr != nil {
				ui.Warnf("Could not remove old temp dir: %v\n", rmErr)
			}
		}

		// Per-item context derived from the batch context, with cancel
		// causes to distinguish skip vs abort.
		itemCtx, cancelItem := context.WithCancelCause(ctx)

		var verifyOK bool
		var verifyDetail string
		var didVerify bool

		runErr := ui.RunWithTUI(ui.RunOptions{
			Ctx: itemCtx,
			OnSkip: func() {
				cancelItem(downloader.ErrSkipCurrent)
			},
			OnQuit: func() {
				// 'q' in batch mode aborts the entire batch — cancel the
				// parent context indirectly by cancelling this item with
				// ErrAbortBatch and signalling the outer loop below.
				cancelItem(downloader.ErrAbortBatch)
			},
			NumConns:     conn,
			WillVerify:   verify,
			BatchCurrent: i + 1,
			BatchTotal:   len(items),
		}, func() error {
			if err := downloader.Execute(itemCtx, it.url, nil, conn, skiptls, proxy, bwLimit, timeout); err != nil {
				return err
			}
			if verify {
				verifyOK, verifyDetail = downloader.RunVerify(it.url, skiptls, proxy, timeout)
				didVerify = true
				if !verifyOK {
					return fmt.Errorf("signature: %s", verifyDetail)
				}
			}
			return nil
		})
		cancelItem(nil) // release goroutine in WithCancelCause

		if didVerify {
			ui.PrintVerifySummary(verifyOK, verifyDetail)
		}

		// Classify the outcome.
		switch {
		case runErr == nil:
			it.status = statusDone
		case errors.Is(runErr, downloader.ErrSkipCurrent):
			it.status = statusSkipped
			it.reason = "user skipped"
		case errors.Is(runErr, downloader.ErrAbortBatch):
			it.status = statusAborted
			it.reason = "user aborted"
			// Mark all remaining as aborted and break.
			for j := i + 1; j < len(items); j++ {
				items[j].status = statusAborted
			}
			itemReason = ""
			fmt.Println()
			printQueuePanel(-1)
			return
		case errors.Is(runErr, context.Canceled):
			it.status = statusAborted
			it.reason = "cancelled"
		default:
			it.status = statusFailed
			itemReason = runErr.Error()
			it.reason = itemReason
		}
		fmt.Println()

		// If the *parent* (batch) context got cancelled mid-item (external
		// SIGINT), break out — don't start another download.
		if ctx.Err() != nil {
			for j := i + 1; j < len(items); j++ {
				items[j].status = statusAborted
			}
			break
		}
	}

	// ── Final summary panel ───────────────────────────────────────────────────
	printQueuePanel(-1)

	done, failed, aborted := 0, 0, 0
	for _, it := range items {
		switch it.status {
		case statusDone, statusSkipped:
			done++
		case statusFailed:
			failed++
		case statusAborted:
			aborted++
		}
	}
	switch {
	case aborted > 0 && failed == 0:
		fmt.Println(styleAbort.Render(fmt.Sprintf("  ⊘  Aborted — %d/%d completed.", done, len(items))))
	case failed == 0:
		fmt.Println(styleDone.Render(fmt.Sprintf("  ✓  All %d downloads complete.", len(items))))
	default:
		fmt.Println(styleFail.Render(fmt.Sprintf("  ✗  %d/%d failed%s.", failed, len(items),
			func() string {
				if aborted > 0 {
					return fmt.Sprintf(", %d aborted", aborted)
				}
				return ""
			}())))
	}
	fmt.Println()
}

func truncateSummary(s string, maxLen int) string {
	if idx := strings.IndexByte(s, '\n'); idx != -1 {
		s = s[:idx]
	}
	s = strings.TrimSpace(s)
	if len(s) > maxLen {
		return s[:maxLen-1] + "…"
	}
	return s
}
