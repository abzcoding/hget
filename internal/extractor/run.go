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
	// Surface the format table separately so the VCR can drop into
	// browsing mode.  Audio-only formats are an optional rocker — if
	// the list is empty the UI hides that switch entirely.
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
// HTTP downloads.  `opts` carries optional auth (cookies file / browser).
//
// `selector`, when non-nil, is invoked after Probe with the resolved
// metadata.  Callers wire this to the TUI's format browser so the user
// picks a tape before yt-dlp engages.  A nil selector (or one returning
// FormatSelection{}) falls through to yt-dlp's bv*+ba/b default.
func Pipeline(ctx context.Context, url, outDir string, opts Options, selector SelectorFunc) error {
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
