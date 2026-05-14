package extractor

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/abzcoding/hget/internal/ui"
)

// BatchPolicy controls how the format selector interacts with the queue.
type BatchPolicy int

const (
	// BatchFormatAll — the user picks once on the first tape; that
	// FormatSelection is reused for every subsequent tape.  Default.
	BatchFormatAll BatchPolicy = iota

	// BatchFormatEach — the deck halts at READY for every tape.
	// Slow but precise.  Use when each video has different needs.
	BatchFormatEach

	// BatchFormatPreset — never browse.  Selector is ignored; yt-dlp
	// runs with its default spec (or the spec the caller has wired in
	// at the extractor.Run level).  Used by --format=<spec> and the
	// non-TTY fallback.
	BatchFormatPreset
)

// BatchPipeline orchestrates a yt-dlp run over a list of URLs.  Flow:
//
//  1. Seed the cassette shelf (one CassetteItem per URL).
//  2. Spawn an eager parallel probe pool (size 3) so spine labels
//     resolve before the deck reaches their position.
//  3. Sequentially activate each cassette: drain its probe result,
//     run the selector under policy, fire Run().
//
// `enableBrowsing` controls whether tape #1 may stop at READY for the
// user to manipulate the rockers.  When false the selector is expected
// to return a preset immediately and the rocker UI never appears.
//
// Failures don't abort the queue — each tape's outcome is recorded on
// the shelf so the end-of-batch summary can list errors.  ctx
// cancellation aborts the whole batch.
func BatchPipeline(ctx context.Context, urls []string, outDir string, opts Options, policy BatchPolicy, enableBrowsing bool, selector SelectorFunc) error {
	if len(urls) == 0 {
		return errors.New("batch: empty URL list")
	}

	// 1. Seed the shelf so the user sees the queue immediately.
	if ui.Program != nil {
		ui.Program.Send(ui.ExtractorShelfSeedMsg{URLs: urls})
	}

	// 2. Eager parallel probes.  Each probe result lands in `probes[i]`
	//    keyed by the URL's original position so the sequential player
	//    loop below can drain them in order.
	probes := newProbeFanout(len(urls))
	go runProbeFanout(ctx, urls, opts, probes)

	// 3. Wrap the selector with the batch policy.  After the first
	//    successful selection under BatchFormatAll, subsequent calls
	//    return the saved value without firing the UI.
	scopedSelector := wrapBatchSelector(policy, selector)

	var firstErr error

	// 4. Sequential player loop.
	for i, url := range urls {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Set this cassette as active before anything else — animates
		// the lift even while we're waiting on a slow probe.
		if ui.Program != nil {
			ui.Program.Send(ui.ExtractorShelfActiveMsg{Index: i})
			ui.Program.Send(ui.ExtractorURLMsg{URL: url})
			ui.Program.Send(ui.ExtractorResetDeckMsg{})
		}

		// Block on probe result.  Errors are recorded on the shelf and
		// we continue to the next URL.
		res, ok := probes.await(ctx, i)
		if !ok {
			return ctx.Err()
		}
		if res.err != nil {
			markCassette(i, ui.CassetteFailed, res.err.Error())
			if firstErr == nil {
				firstErr = res.err
			}
			continue
		}

		// Forward the probe metadata to the VCR + shelf.
		pushMetaToUI(i, res.meta)

		// Selector phase.  We always invoke the selector — when
		// browsing is disabled the selector is a fast preset-returning
		// function rather than a UI block.  Only skip entirely on live
		// streams (yt-dlp picks "best" for them anyway).
		var sel FormatSelection
		if scopedSelector != nil && !res.meta.IsLive {
			if enableBrowsing && len(res.meta.Formats) > 0 {
				markCassette(i, ui.CassetteReady, "")
			}
			sel, _ = scopedSelector(ctx, res.meta)
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}

		// Engage tape.
		markCassette(i, ui.CassetteLoading, "")
		sink := &uiSink{enableBrowsing: enableBrowsing}
		// We've already sent the meta to the UI via pushMetaToUI; tell
		// the sink to skip re-emitting it so the VCR doesn't re-seed.
		sink.metaAlreadySent = true
		sink.meta = res.meta

		_, runErr := Run(ctx, url, outDir, opts, sel, sink)
		if runErr != nil {
			markCassette(i, ui.CassetteFailed, runErr.Error())
			if errors.Is(runErr, context.Canceled) {
				return runErr
			}
			if firstErr == nil {
				firstErr = runErr
			}
			continue
		}
		markCassette(i, ui.CassetteDone, "")
	}

	// 5. Final shelf state: no tape active anymore.
	if ui.Program != nil {
		ui.Program.Send(ui.ExtractorShelfActiveMsg{Index: -1})
	}

	return firstErr
}

// pushMetaToUI fires the same ExtractorMetaMsg + ExtractorFormatsMsg
// the single-URL pipeline emits, plus the shelf-level meta update.
func pushMetaToUI(i int, m Meta) {
	if ui.Program == nil {
		return
	}
	ui.Program.Send(ui.ExtractorShelfMetaMsg{
		Index:      i,
		Title:      m.Title,
		Channel:    m.Uploader,
		Resolution: m.Resolution,
		Duration:   m.Duration,
	})
	ui.Program.Send(ui.ExtractorMetaMsg{
		Title:      m.Title,
		Channel:    m.Uploader,
		Duration:   m.Duration,
		Resolution: m.Resolution,
		FPS:        m.FPS,
		VCodec:     m.VCodec,
		ACodec:     m.ACodec,
		Container:  m.Container,
		HasAudio:   m.NeedsMux() || (m.ACodec != "" && m.ACodec != "none"),
		OutputFile: m.SafeFilename(),
	})
	if !m.IsLive && len(m.Formats) > 0 {
		ui.Program.Send(ui.ExtractorFormatsMsg{
			Video:      toUIFormats(m.VideoFormats()),
			Audio:      toUIFormats(m.AudioFormats()),
			Containers: []string{"mp4", "mkv", "webm"},
			IsLive:     m.IsLive,
		})
	}
}

func markCassette(i int, st ui.CassetteStatus, errMsg string) {
	if ui.Program == nil {
		return
	}
	ui.Program.Send(ui.ExtractorShelfStatusMsg{Index: i, Status: st, Err: errMsg})
}

// wrapBatchSelector applies the policy to a single-shot SelectorFunc.
func wrapBatchSelector(policy BatchPolicy, sel SelectorFunc) SelectorFunc {
	if sel == nil {
		return nil
	}
	if policy == BatchFormatPreset {
		return func(ctx context.Context, _ Meta) (FormatSelection, error) {
			return FormatSelection{}, nil
		}
	}
	if policy == BatchFormatEach {
		return sel
	}
	// BatchFormatAll: cache the first successful selection, then on
	// every reuse drop the exact Spec so the adaptive Pref drives the
	// format expression.  Tape #1 itself uses the verbatim Spec (it
	// was picked from its own format list, so an exact match is safe).
	var (
		mu     sync.Mutex
		cached FormatSelection
		hasIt  bool
	)
	return func(ctx context.Context, meta Meta) (FormatSelection, error) {
		mu.Lock()
		if hasIt {
			adaptive := cached
			adaptive.Spec = "" // force Pref.AdaptiveSpec() in Args()
			mu.Unlock()
			return adaptive, nil
		}
		mu.Unlock()
		s, err := sel(ctx, meta)
		if err != nil {
			return s, err
		}
		mu.Lock()
		cached, hasIt = s, true
		mu.Unlock()
		return s, nil
	}
}

// ── Probe fan-out ───────────────────────────────────────────────────────────

// probeResult is one slot in the parallel probe table.
type probeResult struct {
	meta Meta
	err  error
	done chan struct{} // closed once meta / err are populated
}

type probeFanout struct {
	results []probeResult
}

func newProbeFanout(n int) *probeFanout {
	r := &probeFanout{results: make([]probeResult, n)}
	for i := range r.results {
		r.results[i].done = make(chan struct{})
	}
	return r
}

func (f *probeFanout) finish(i int, m Meta, err error) {
	f.results[i].meta = m
	f.results[i].err = err
	close(f.results[i].done)
}

// await blocks until probe i has resolved or ctx is cancelled.  Second
// return is false on cancellation.
func (f *probeFanout) await(ctx context.Context, i int) (probeResult, bool) {
	select {
	case <-f.results[i].done:
		return f.results[i], true
	case <-ctx.Done():
		return probeResult{}, false
	}
}

// runProbeFanout spawns up to `probeWorkers` goroutines that drain the
// URL list serially-by-position but in parallel across workers.  Probe
// statuses propagate to the shelf so spine labels animate during load.
const probeWorkers = 3

func runProbeFanout(ctx context.Context, urls []string, opts Options, out *probeFanout) {
	jobs := make(chan int, len(urls))
	for i := range urls {
		jobs <- i
	}
	close(jobs)

	var wg sync.WaitGroup
	wg.Add(probeWorkers)
	for w := 0; w < probeWorkers; w++ {
		go func() {
			defer wg.Done()
			for i := range jobs {
				if ctx.Err() != nil {
					out.finish(i, Meta{}, ctx.Err())
					continue
				}
				markCassette(i, ui.CassetteProbing, "")
				probeCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
				m, err := Probe(probeCtx, urls[i], opts)
				cancel()
				if err != nil {
					out.finish(i, Meta{}, err)
					continue
				}
				// Push partial metadata to the shelf immediately so the
				// spine populates before its turn at the deck.
				if ui.Program != nil {
					ui.Program.Send(ui.ExtractorShelfMetaMsg{
						Index:      i,
						Title:      m.Title,
						Channel:    m.Uploader,
						Resolution: m.Resolution,
						Duration:   m.Duration,
					})
				}
				markCassette(i, ui.CassetteQueued, "")
				out.finish(i, m, nil)
			}
		}()
	}
	wg.Wait()
}

// BatchSummary collects per-item outcomes for the end-of-batch report.
// Returned by the runner so main.go can print a clean tally.
type BatchSummary struct {
	Total   int
	Done    int
	Failed  int
	Skipped int
	Errors  []string // "<url>: <err>" — one per failure
}

// SummariseShelf walks the final shelf state and produces a BatchSummary
// for the post-run report.  Pure function — extracted for testability.
func SummariseShelf(items []ui.CassetteItem) BatchSummary {
	s := BatchSummary{Total: len(items)}
	for _, it := range items {
		switch it.Status {
		case ui.CassetteDone:
			s.Done++
		case ui.CassetteFailed:
			s.Failed++
			s.Errors = append(s.Errors, fmt.Sprintf("%s: %s", it.URL, it.Err))
		case ui.CassetteSkipped:
			s.Skipped++
		}
	}
	return s
}
