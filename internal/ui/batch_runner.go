package ui

import (
	"context"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"
)

// BatchRunOptions configures a persistent batch TUI session.
type BatchRunOptions struct {
	Ctx           context.Context
	NumConns      int
	WillVerify    bool
	Items         []BatchItemSnapshot
	URLs          []string
	OnQuit        func()
	OnSkipCurrent func()
}

type BatchItemResult struct {
	Status BatchItemStatus
	Reason string
}

// BatchSummary aggregates the tally over the whole batch.
type BatchSummary struct {
	Done    int
	Failed  int
	Skipped int
	Aborted int
}

func RunBatch(opts BatchRunOptions, runItem func(idx int) BatchItemResult) (BatchSummary, error) {
	if !isatty.IsTerminal(os.Stdout.Fd()) || !DisplayProgress {
		// Headless fallback: just run items sequentially.
		summary := BatchSummary{}
		for i, it := range opts.Items {
			if opts.Ctx != nil && opts.Ctx.Err() != nil {
				summary.Aborted += len(opts.Items) - i
				break
			}
			if it.Status == BatchItemSkipped {
				summary.Skipped++
				continue
			}
			r := runItem(i)
			switch r.Status {
			case BatchItemDone:
				summary.Done++
			case BatchItemFailed:
				summary.Failed++
			case BatchItemSkipped:
				summary.Skipped++
			case BatchItemAborted:
				summary.Aborted++
				// Mark rest as aborted.
				for j := i + 1; j < len(opts.Items); j++ {
					summary.Aborted++
				}
				return summary, nil
			}
		}
		return summary, nil
	}

	model := NewTUIModelWithHistory(
		opts.NumConns,
		opts.WillVerify,
		1,
		len(opts.Items),
		opts.Items,
		opts.OnSkipCurrent,
		opts.OnQuit,
	)
	model.batchMode = true
	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithoutSignalHandler())
	Program = p

	stopWatch := make(chan struct{})
	if opts.Ctx != nil {
		go func() {
			select {
			case <-opts.Ctx.Done():
				p.Send(StoppingMsg{Reason: "Aborted — finishing current item"})
			case <-stopWatch:
			}
		}()
	}

	summaryCh := make(chan BatchSummary, 1)
	go func() {
		summary := BatchSummary{}
		defer func() {
			p.Send(BatchFinishedMsg{
				Done:    summary.Done,
				Failed:  summary.Failed,
				Skipped: summary.Skipped,
				Aborted: summary.Aborted,
			})
			summaryCh <- summary
		}()

		for i, it := range opts.Items {
			if opts.Ctx != nil && opts.Ctx.Err() != nil {
				for j := i; j < len(opts.Items); j++ {
					if opts.Items[j].Status == BatchItemSkipped {
						p.Send(BatchItemEndMsg{Index: j, Status: BatchItemSkipped})
						summary.Skipped++
						continue
					}
					p.Send(BatchItemEndMsg{Index: j, Status: BatchItemAborted})
					summary.Aborted++
				}
				return
			}

			if it.Status == BatchItemSkipped {
				p.Send(BatchItemEndMsg{Index: i, Status: BatchItemSkipped, Reason: "already exists"})
				summary.Skipped++
				continue
			}

			url := ""
			if i < len(opts.URLs) {
				url = opts.URLs[i]
			}
			p.Send(BatchItemBeginMsg{Index: i, URL: url, FileName: it.Label})
			r := runItem(i)
			p.Send(BatchItemEndMsg{Index: i, Status: r.Status, Reason: r.Reason})

			switch r.Status {
			case BatchItemDone:
				summary.Done++
			case BatchItemSkipped:
				summary.Skipped++
			case BatchItemFailed:
				summary.Failed++
			case BatchItemAborted:
				summary.Aborted++
				for j := i + 1; j < len(opts.Items); j++ {
					p.Send(BatchItemEndMsg{Index: j, Status: BatchItemAborted})
					summary.Aborted++
				}
				return
			}
		}
	}()

	_, runErr := p.Run()
	Program = nil
	close(stopWatch)
	summary := <-summaryCh

	if runErr != nil {
		return summary, fmt.Errorf("tui: %w", runErr)
	}
	return summary, nil
}
