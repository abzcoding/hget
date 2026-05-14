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
type uiSink struct {
	meta Meta
	// metaAlreadySent suppresses the re-broadcast of ExtractorMetaMsg
	// during Run() when the caller (e.g. BatchPipeline) has already
	// pushed metadata to the UI between probe and run.  Avoids a flash
	// of duplicate "0%" updates.
	metaAlreadySent bool
	// enableBrowsing controls whether the sink emits the
	// ExtractorFormatsMsg that puts the VCR into rocker-browsing
	// mode.  False by default — the hget CLI uses a quality preset
	// (720p mp4) and only sets this when the user passes
	// `--pick-format` to opt into manual selection.
	enableBrowsing bool
}

func (s *uiSink) OnMeta(m Meta) {
	s.meta = m
	if ui.Program == nil || s.metaAlreadySent {
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
	// Surface the format table separately so the VCR can drop into
	// browsing mode — but only when the caller opted in.  When the
	// quality preset path is active we never want the rocker UI to
	// appear: the VCR slides straight from standby into recording.
	if !s.enableBrowsing {
		return
	}
	video := m.VideoFormats()
	audio := m.AudioFormats()
	if len(video) == 0 && len(audio) == 0 {
		return
	}
	ui.Program.Send(ui.ExtractorFormatsMsg{
		Video:      toUIFormats(video),
		Audio:      toUIFormats(audio),
		Containers: []string{"mp4", "mkv", "webm"},
		IsLive:     m.IsLive,
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

// toUIFormats translates extractor.Format → ui.ExtractorFormat without
// dragging extractor types into the ui package (which can't import us).
func toUIFormats(in []Format) []ui.ExtractorFormat {
	out := make([]ui.ExtractorFormat, 0, len(in))
	for _, f := range in {
		out = append(out, ui.ExtractorFormat{
			ID:         f.ID,
			Ext:        f.Ext,
			Resolution: f.Resolution,
			Height:     f.Height,
			FPS:        f.FPS,
			VCodec:     f.VCodec,
			ACodec:     f.ACodec,
			TBR:        f.TBR,
			ABR:        f.ABR,
			Filesize:   f.Filesize,
			Note:       f.Note,
			HasVideo:   f.HasVideo,
			HasAudio:   f.HasAudio,
		})
	}
	return out
}

// Pipeline runs the full extractor pipeline: probe → select → download
// → mux → done.  The TUI is started by RunExtractorTUI; this function
// is the worker passed to it.  It blocks until the yt-dlp child exits
// or ctx is cancelled.
//
// `outDir` is forwarded to yt-dlp via -P (download path).  Empty means
// "current working directory" — matching hget's existing behaviour for
// HTTP downloads.  `opts` carries optional auth (cookies file / browser)
// and the language preference forwarded as `-S lang:<pref>`.
//
// `enableBrowsing` controls whether the TUI is allowed to drop into
// rocker-selection mode after the probe.  When false, the rocker UI
// is suppressed entirely and the selector is expected to return a
// preset immediately without blocking.
//
// `selector`, when non-nil, is invoked after Probe with the resolved
// metadata.  Callers wire this either to the TUI's format browser
// (browsing path) or to a preset-returning fast path (non-browsing).
// A nil selector falls through to yt-dlp's bv*+ba/b default.
func Pipeline(ctx context.Context, url, outDir string, opts Options, enableBrowsing bool, selector SelectorFunc) error {
	sink := &uiSink{enableBrowsing: enableBrowsing}

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

	// ── Selection phase. ────────────────────────────────────────────
	// Skipped when:
	//   * selector is nil (non-TTY or --format already supplied)
	//   * stream is live — no fixed format list to browse
	//   * formats slice is empty — yt-dlp didn't expose one (rare;
	//     some single-stream sources)
	var sel FormatSelection
	if selector != nil && !meta.IsLive && len(meta.Formats) > 0 {
		sel, err = selector(ctx, meta)
		if err != nil {
			return err
		}
	}

	// ── Run yt-dlp. ─────────────────────────────────────────────────
	if _, err := Run(ctx, url, outDir, opts, sel, sink); err != nil {
		return err
	}
	return nil
}
