package batch

import (
	"bufio"
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
// printing a live queue panel before each download and a final summary afterwards.
func RunBatchDownloads(filePath string, conn int, skiptls bool, proxy, bwLimit string, timeout time.Duration, verify bool) {
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
				if verify {
					ok, detail := downloader.RunVerify(it.url, skiptls, proxy, timeout)
					ui.PrintVerifySummary(ok, detail)
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

		// Remove stale temp dir.
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
			ui.RunWithTUI(func() {
				downloader.Execute(it.url, nil, conn, skiptls, proxy, bwLimit, timeout)
				if verify {
					verifyOK, verifyDetail = downloader.RunVerify(it.url, skiptls, proxy, timeout)
					didVerify = true
				}
			}, conn, verify, i+1, len(items))
		}()

		if didVerify {
			ui.PrintVerifySummary(verifyOK, verifyDetail)
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

	done, failed := 0, 0
	for _, it := range items {
		switch it.status {
		case statusDone, statusSkipped:
			done++
		case statusFailed:
			failed++
		}
	}
	_ = done
	if failed == 0 {
		fmt.Println(styleDone.Render(fmt.Sprintf("  ✓  All %d downloads complete.", len(items))))
	} else {
		fmt.Println(styleFail.Render(fmt.Sprintf("  ✗  %d/%d failed.", failed, len(items))))
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
