package extractor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/abzcoding/hget/internal/ui"
)

// uiSink adapts the extractor's MetaSink interface onto the active TUI
// program by translating each event into a ui.Extractor* Tea message.
// When ui.Program is nil (no TUI), events are dropped — caller is
// expected to print a friendly summary in that path.
type uiSink struct{ meta Meta }

func (s *uiSink) OnMeta(m Meta) {
	s.meta = m
	if ui.Program == nil {
		return
	}
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
}

func (s *uiSink) OnDownloadProgress(p DownloadProgress) {
	if ui.Program == nil {
		return
	}
	ui.Program.Send(ui.ExtractorProgressMsg{
		Percent:    p.Percent,
		Downloaded: p.Downloaded,
		Total:      p.Total,
		SpeedBPS:   p.SpeedBPS,
		ETA:        p.ETA,
		Fragment:   p.Fragment,
		FragmentN:  p.FragmentN,
	})
}

func (s *uiSink) OnPhaseChange(ph Phase) {
	if ui.Program == nil {
		return
	}
	switch ph {
	case PhaseDownloading:
		ui.Program.Send(ui.ExtractorPhaseMsg{Phase: "downloading"})
	case PhaseMuxing:
		ui.Program.Send(ui.ExtractorPhaseMsg{Phase: "muxing"})
	case PhaseDone:
		// 'done' is sent by the TUI runner after worker returns nil so
		// it lines up with the actual exit code; we don't fire it here.
	case PhaseError:
		// likewise — the worker's returned error drives the error UI.
	}
}

func (s *uiSink) OnLog(level, line string) {
	if ui.Program == nil {
		return
	}
	ui.Program.Send(ui.ExtractorLogMsg{Level: level, Text: line})
}

// Pipeline runs the full extractor pipeline: probe → download/mux → done.
// The TUI is started by RunExtractorTUI; this function is the worker
// passed to it.  It blocks until the yt-dlp child exits or ctx is
// cancelled.
//
// `outDir` is forwarded to yt-dlp via -P (download path).  Empty means
// "current working directory" — matching hget's existing behaviour for
// HTTP downloads.  `opts` carries optional auth (cookies file / browser).
func Pipeline(ctx context.Context, url, outDir string, opts Options) error {
	sink := &uiSink{}

	// ── Probe phase. ────────────────────────────────────────────────
	// Probe is fast (single HTTP roundtrip) — so we wrap it in a tight
	// timeout so a wedged extractor process can't hang the TUI before
	// the user even sees a frame.  60s is generous: YouTube's bot
	// challenge can add several seconds to the initial extraction even
	// when valid cookies are supplied.
	probeCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	meta, err := Probe(probeCtx, url, opts)
	cancel()
	if err != nil {
		if errors.Is(err, ErrNotInstalled) {
			return fmt.Errorf("%w — install with `brew install yt-dlp` (macOS) or `pipx install yt-dlp`", err)
		}
		return err
	}
	sink.OnMeta(meta)

	// ── Run yt-dlp. ─────────────────────────────────────────────────
	if _, err := Run(ctx, url, outDir, opts, sink); err != nil {
		return err
	}
	return nil
}
