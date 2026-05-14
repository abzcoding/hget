package batch

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/abzcoding/hget/internal/downloader"
	"github.com/abzcoding/hget/internal/state"
	"github.com/abzcoding/hget/internal/ui"
	"github.com/abzcoding/hget/internal/util"
)

type prepItem struct {
	url         string
	file        string
	preSkip     bool // user declined re-download during upfront prompt
	skipReason  string
	resumeState *state.State
}

func RunBatchDownloads(ctx context.Context, filePath string, conn int, skiptls bool, proxy, bwLimit string, timeout time.Duration, verify bool) {
	// ── Read and validate URL list ────────────────────────────────────────────
	f, err := os.Open(filePath)
	if err != nil {
		ui.ShowMessage(ui.MessageError, "FILE ERROR", fmt.Sprintf("Could not open URL list: %s\n%v", filePath, err))
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
		ui.ShowMessage(ui.MessageError, "READ ERROR", fmt.Sprintf("Error reading file: %s\n%v", filePath, scanErr))
		return
	}
	if len(urls) == 0 {
		ui.ShowMessage(ui.MessageWarning, "EMPTY FILE", fmt.Sprintf("No URLs found in: %s", filePath))
		return
	}

	// ── Phase 1: collect every user decision up-front (overwrite, resume) ────
	prep := prepareBatch(urls, verify)

	// Build the SAN seed list (one bay per URL) and a parallel URL list.
	seed := make([]ui.BatchItemSnapshot, len(prep))
	urlList := make([]string, len(prep))
	for i, p := range prep {
		status := ui.BatchItemQueued
		if p.preSkip {
			status = ui.BatchItemSkipped
		}
		seed[i] = ui.BatchItemSnapshot{Label: p.file, Status: status}
		urlList[i] = p.url
	}

	// ── Phase 2: single persistent TUI drives every download ─────────────────
	var (
		mu            sync.Mutex
		currentCancel func(error)
	)
	skipCurrent := func() {
		mu.Lock()
		defer mu.Unlock()
		if currentCancel != nil {
			currentCancel(downloader.ErrSkipCurrent)
		}
	}
	quitBatch := func() {
		mu.Lock()
		defer mu.Unlock()
		if currentCancel != nil {
			currentCancel(downloader.ErrAbortBatch)
		}
	}

	runItem := func(idx int) ui.BatchItemResult {
		pi := prep[idx]
		itemCtx, cancelItem := context.WithCancelCause(ctx)
		mu.Lock()
		currentCancel = cancelItem
		mu.Unlock()
		defer func() {
			mu.Lock()
			currentCancel = nil
			mu.Unlock()
			cancelItem(nil)
		}()

		if err := downloader.Execute(itemCtx, pi.url, pi.resumeState, conn, skiptls, proxy, bwLimit, timeout); err != nil {
			switch {
			case errors.Is(err, downloader.ErrSkipCurrent):
				return ui.BatchItemResult{Status: ui.BatchItemSkipped, Reason: "user skipped"}
			case errors.Is(err, downloader.ErrAbortBatch):
				return ui.BatchItemResult{Status: ui.BatchItemAborted, Reason: "user aborted"}
			case errors.Is(err, context.Canceled):
				return ui.BatchItemResult{Status: ui.BatchItemAborted, Reason: "cancelled"}
			default:
				return ui.BatchItemResult{Status: ui.BatchItemFailed, Reason: err.Error()}
			}
		}
		if verify {
			ok, detail := downloader.RunVerify(itemCtx, pi.url, skiptls, proxy, timeout)
			if !ok {
				return ui.BatchItemResult{Status: ui.BatchItemFailed, Reason: "signature: " + detail}
			}
		}
		return ui.BatchItemResult{Status: ui.BatchItemDone}
	}

	summary, _ := ui.RunBatch(ui.BatchRunOptions{
		Ctx:           ctx,
		NumConns:      conn,
		WillVerify:    verify,
		Items:         seed,
		URLs:          urlList,
		OnQuit:        quitBatch,
		OnSkipCurrent: skipCurrent,
	}, runItem)

	// ── Phase 3: post-TUI line summary (logger handles its own colouring) ────
	cMint := ui.Theme.Mint
	cMag := ui.Theme.Magenta
	cAmber := ui.Theme.Amber

	total := len(prep)
	switch {
	case summary.Aborted > 0 && summary.Failed == 0:
		fmt.Println(lipgloss.NewStyle().Foreground(cAmber).Bold(true).
			Render(fmt.Sprintf("  ⊘  aborted — %d/%d completed", summary.Done+summary.Skipped, total)))
	case summary.Failed == 0:
		fmt.Println(lipgloss.NewStyle().Foreground(cMint).Bold(true).
			Render(fmt.Sprintf("  ⬢  %d/%d transfers complete", summary.Done+summary.Skipped, total)))
	default:
		extra := ""
		if summary.Aborted > 0 {
			extra = fmt.Sprintf(", %d aborted", summary.Aborted)
		}
		fmt.Println(lipgloss.NewStyle().Foreground(cMag).Bold(true).
			Render(fmt.Sprintf("  ◈  %d/%d failed%s", summary.Failed, total, extra)))
	}
}

func prepareBatch(urls []string, verify bool) []prepItem {
	_ = verify
	out := make([]prepItem, len(urls))
	for i, u := range urls {
		out[i] = prepItem{url: u, file: util.TaskFromURL(u)}

		// File-exists prompt (huh inline form — no alt-screen).
		if _, statErr := os.Stat(out[i].file); statErr == nil {
			if !ui.ConfirmRedownload(out[i].file) {
				out[i].preSkip = true
				out[i].skipReason = "already exists"
				continue
			}
			// User chose to overwrite — wipe any stale per-URL state.
			if util.ExistDir(state.FolderOf(u)) {
				if rmErr := os.RemoveAll(state.FolderOf(u)); rmErr != nil {
					ui.Warnf("Could not remove old state: %v\n", rmErr)
				}
			}
		}

		// Resume prompt for partial downloads.
		if state.Exists(u) {
			st, _ := state.PromptResume(u)
			out[i].resumeState = st
		}
	}
	return out
}
