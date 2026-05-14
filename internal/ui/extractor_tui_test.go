package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// step routes a sequence of Tea messages through the model and returns
// the resulting view.  Mirrors what the live program does, minus the
// real timer goroutine.
func step(t *testing.T, m extractorModel, msgs ...tea.Msg) extractorModel {
	t.Helper()
	var mod tea.Model = m
	for _, msg := range msgs {
		mod, _ = mod.Update(msg)
	}
	return mod.(extractorModel)
}

// tickN advances the animation N times so spring physics settle.
func tickN(t *testing.T, m extractorModel, n int) extractorModel {
	t.Helper()
	for i := 0; i < n; i++ {
		m = step(t, m, extractorTickMsg(time.Now()))
	}
	return m
}

func TestExtractorModel_RendersMetaInVCRPanel(t *testing.T) {
	m := NewExtractorModel("https://vimeo.com/76979871", func() {})
	m.width = 100
	m.height = 60

	m = step(t, m,
		ExtractorMetaMsg{
			Title:      "The New Vimeo Player",
			Channel:    "Vimeo Staff",
			Duration:   2*time.Minute + 30*time.Second,
			Resolution: "1280x720",
			FPS:        30,
			VCodec:     "avc1.42001f",
			ACodec:     "mp4a.40.2",
			Container:  "mp4",
			HasAudio:   true,
			OutputFile: "The New Vimeo Player.mp4",
		},
		ExtractorPhaseMsg{Phase: "downloading"},
	)
	m = tickN(t, m, 5)

	view := m.View()
	mustContain(t, view, "HGET·VCR/4-HEAD", "VCR brand plate missing")
	mustContain(t, view, "TRANSPORT", "TRANSPORT label missing")
	mustContain(t, view, "The New Vimeo Player", "title row missing")
	mustContain(t, view, "Vimeo Staff", "channel row missing")
	mustContain(t, view, "1280x720", "resolution missing from video row")
	mustContain(t, view, "30fps", "fps missing from video row")
	mustContain(t, view, "avc1.42001f", "video codec missing")
	mustContain(t, view, "mp4a.40.2", "audio codec missing")
	mustContain(t, view, "00:02:30", "total duration missing from counter")
	mustContain(t, view, "● REC", "REC indicator missing from progress row")
	mustContain(t, view, "AUDIO  L", "VU meter L channel missing")
	mustContain(t, view, "AUDIO", "AUDIO label missing")
	mustContain(t, view, "vimeo.com/76979871", "source URL missing")
}

func TestExtractorModel_ProgressMessagesAdvanceTheBar(t *testing.T) {
	m := NewExtractorModel("https://vimeo.com/76979871", func() {})
	m.width = 100
	m.height = 60

	// Seed metadata so the panel has stable context.
	m = step(t, m,
		ExtractorMetaMsg{Title: "X", Container: "mp4", HasAudio: true, Duration: time.Minute},
		ExtractorPhaseMsg{Phase: "downloading"},
	)

	// 0% → 50% → 100% — settle the spring after each step.
	m = step(t, m, ExtractorProgressMsg{
		Percent: 0.0, Downloaded: 0, Total: 1_000_000, SpeedBPS: 0,
	})
	m = tickN(t, m, 60)
	v0 := m.View()

	m = step(t, m, ExtractorProgressMsg{
		Percent: 50.0, Downloaded: 500_000, Total: 1_000_000, SpeedBPS: 1_500_000,
		ETA: 5 * time.Second,
	})
	m = tickN(t, m, 90) // give the harmonic spring time to settle near target
	v50 := m.View()

	m = step(t, m, ExtractorProgressMsg{
		Percent: 100.0, Downloaded: 1_000_000, Total: 1_000_000, SpeedBPS: 1_500_000,
	})
	m = tickN(t, m, 90)
	v100 := m.View()

	// The percent string in the progress row must change monotonically.
	pct0 := extractPct(t, v0)
	pct50 := extractPct(t, v50)
	pct100 := extractPct(t, v100)
	t.Logf("rendered percentages: %.1f → %.1f → %.1f", pct0, pct50, pct100)
	if !(pct0 < pct50 && pct50 < pct100) {
		t.Errorf("expected monotonic %% (got %.1f, %.1f, %.1f)", pct0, pct50, pct100)
	}
	if pct100 < 99.0 {
		t.Errorf("expected ~100%% after settling, got %.1f", pct100)
	}

	// Rate row shows the speed at 50%.
	mustContain(t, v50, "1.4 MB/s", "rate row missing speed at 50%")
	mustContain(t, v50, "488.3 KB of 976.6 KB", "rate row missing byte-count at 50%")
}

func TestExtractorModel_FragmentReadout(t *testing.T) {
	m := NewExtractorModel("https://example.com/m3u8", func() {})
	m.width = 100
	m.height = 60
	m = step(t, m,
		ExtractorMetaMsg{Title: "HLS Stream", HasAudio: true, Container: "mp4"},
		ExtractorPhaseMsg{Phase: "downloading"},
		ExtractorProgressMsg{Percent: 25, Downloaded: 100, Total: 400, Fragment: 137, FragmentN: 320, SpeedBPS: 50_000},
	)
	m = tickN(t, m, 5)
	view := m.View()
	mustContain(t, view, "FRAG", "fragment label missing")
	mustContain(t, view, "0137", "current fragment missing")
	mustContain(t, view, "0320", "total fragments missing")
}

func TestExtractorModel_MuxingPhaseShowsMixerPanel(t *testing.T) {
	m := NewExtractorModel("https://vimeo.com/76979871", func() {})
	m.width = 100
	m.height = 80

	m = step(t, m,
		ExtractorMetaMsg{
			Title: "Sample", VCodec: "vp9", ACodec: "opus", Container: "mp4",
			HasAudio: true, Duration: time.Minute,
		},
		ExtractorPhaseMsg{Phase: "downloading"},
		ExtractorProgressMsg{Percent: 100, Downloaded: 1000, Total: 1000, SpeedBPS: 0},
		ExtractorPhaseMsg{Phase: "muxing"},
	)
	m = tickN(t, m, 20)

	view := m.View()
	mustContain(t, view, "HGET·MIX·M-808", "mixer brand plate missing")
	mustContain(t, view, "CONSOLE", "mixer console label missing")
	mustContain(t, view, "CH 1 VIDEO", "video channel strip missing")
	mustContain(t, view, "CH 2 AUDIO", "audio channel strip missing")
	mustContain(t, view, "MASTER BUS OUT", "master strip missing")
	mustContain(t, view, "muxing", "muxing status text missing")
	mustContain(t, view, "60", "EQ frequency axis missing")
	mustContain(t, view, "16k", "EQ high-frequency band missing")
}

func TestExtractorModel_OutputPathSurfaced(t *testing.T) {
	m := NewExtractorModel("https://vimeo.com/x", func() {})
	m.width = 100
	m.height = 60
	m = step(t, m,
		ExtractorMetaMsg{Title: "X", Container: "mp4", HasAudio: false},
		ExtractorPhaseMsg{Phase: "downloading"},
		ExtractorOutputMsg{Path: "/tmp/Resolved Output Path.mp4"},
	)
	m = tickN(t, m, 5)
	view := m.View()
	mustContain(t, view, "Resolved Output Path.mp4", "resolved output path missing")
}

func TestExtractorModel_BrowsingRockersAndREC(t *testing.T) {
	var committed ExtractorSelectionMsg
	captured := false
	m := NewExtractorModel("https://vimeo.com/76979871", func() {})
	m.SetSelectionCallback(func(s ExtractorSelectionMsg) {
		committed = s
		captured = true
	})
	m.width = 100
	m.height = 60

	// Seed metadata + a two-tier format table (video-only + audio-only).
	m = step(t, m,
		ExtractorMetaMsg{
			Title:    "Sample Video",
			Channel:  "Sample Channel",
			Duration: time.Minute,
			HasAudio: true,
		},
		ExtractorFormatsMsg{
			Video: []ExtractorFormat{
				{ID: "315", Resolution: "3840x2160", Height: 2160, FPS: 60, VCodec: "vp9", Ext: "webm", Filesize: 200_000_000, Note: "2160p60", HasVideo: true},
				{ID: "299", Resolution: "1920x1080", Height: 1080, FPS: 60, VCodec: "avc1", Ext: "mp4", Filesize: 80_000_000, Note: "1080p60", HasVideo: true},
				{ID: "136", Resolution: "1280x720", Height: 720, FPS: 30, VCodec: "avc1", Ext: "mp4", Filesize: 40_000_000, Note: "720p", HasVideo: true},
			},
			Audio: []ExtractorFormat{
				{ID: "251", ACodec: "opus", Ext: "webm", ABR: 160, Filesize: 6_000_000, HasAudio: true},
				{ID: "140", ACodec: "mp4a", Ext: "m4a", ABR: 128, Filesize: 5_000_000, HasAudio: true},
			},
			Containers: []string{"mp4", "mkv", "webm"},
		},
	)
	m = tickN(t, m, 3)

	// The VCR must be in browsing mode showing rocker rows + READY LED.
	view := m.View()
	mustContain(t, view, "READY", "READY LED missing in browsing mode")
	mustContain(t, view, "◀", "left rocker arrow missing")
	mustContain(t, view, "▶", "right rocker arrow missing")
	mustContain(t, view, "2160p60", "highest video preset not shown by default")
	mustContain(t, view, "opus", "default audio not shown")
	mustContain(t, view, "MP4", "default container not shown")
	mustContain(t, view, "ARM", "ARMED progress label missing")
	mustContain(t, view, "▲▼", "browse footer help missing tape rocker hint")

	// Cycle video down once → expect 1080p60 selected.
	m = step(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = tickN(t, m, 2)
	view = m.View()
	mustContain(t, view, "1080p60", "video down-rocker didn't advance")

	// Cycle audio right once → mp4a.
	m = step(t, m, tea.KeyMsg{Type: tea.KeyRight})
	m = tickN(t, m, 2)
	view = m.View()
	mustContain(t, view, "mp4a", "audio rocker didn't advance")

	// Cycle container (tab) → mkv.
	m = step(t, m, tea.KeyMsg{Type: tea.KeyTab})
	m = tickN(t, m, 2)
	view = m.View()
	mustContain(t, view, "MKV", "container rocker didn't advance")

	// Commit with ENTER → callback fires with the expected spec.
	m = step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !captured {
		t.Fatalf("selection callback never fired")
	}
	if committed.Spec != "299+140" {
		t.Errorf("committed spec = %q, want %q", committed.Spec, "299+140")
	}
	if committed.Container != "mkv" {
		t.Errorf("committed container = %q, want mkv", committed.Container)
	}
	// Adaptive descriptors must be populated from the chosen video +
	// audio formats so the batch FormatAll pipeline can fall back to
	// a close match on tapes that lack format IDs 299/140.
	if committed.HeightCeiling != 1080 {
		t.Errorf("HeightCeiling=%d want 1080", committed.HeightCeiling)
	}
	if committed.VCodec != "avc1" {
		t.Errorf("VCodec=%q want avc1", committed.VCodec)
	}
	if committed.ABRCeiling != 128 {
		t.Errorf("ABRCeiling=%d want 128", committed.ABRCeiling)
	}
	if committed.Progressive {
		t.Errorf("Progressive=true for separate v+a pick — should be false")
	}

	// After REC the panel transitions out of browsing — no more ARMED.
	m = tickN(t, m, 3)
	view = m.View()
	if strings.Contains(stripANSI(view), "◐ ARM") {
		t.Errorf("ARMED label still showing after commit")
	}
	mustContain(t, view, "● REC", "REC label didn't engage after commit")
}

func TestExtractorModel_LiveStreamSkipsSelector(t *testing.T) {
	captured := false
	m := NewExtractorModel("https://twitch.tv/x", func() {})
	m.SetSelectionCallback(func(ExtractorSelectionMsg) { captured = true })
	m.width = 100
	m.height = 60

	m = step(t, m,
		ExtractorMetaMsg{Title: "Live Show"},
		ExtractorFormatsMsg{
			Video:  []ExtractorFormat{{ID: "best", Note: "live", HasVideo: true}},
			IsLive: true,
		},
	)
	m = tickN(t, m, 3)
	view := stripANSI(m.View())
	if strings.Contains(view, "◐ ARM") {
		t.Errorf("live stream entered browsing mode")
	}
	if captured {
		t.Errorf("selection callback fired for live stream")
	}
}

func TestExtractorModel_ProgressiveOnlyCollapsesAudioRocker(t *testing.T) {
	m := NewExtractorModel("https://twitter.com/x", func() {})
	m.width = 100
	m.height = 60
	m = step(t, m,
		ExtractorMetaMsg{Title: "Tweet"},
		ExtractorFormatsMsg{
			Video: []ExtractorFormat{
				{ID: "22", Resolution: "1280x720", Height: 720, FPS: 30, VCodec: "avc1", ACodec: "mp4a", Ext: "mp4", HasVideo: true, HasAudio: true},
			},
			Containers: []string{"mp4"},
		},
	)
	m = tickN(t, m, 3)
	view := stripANSI(m.View())
	mustContain(t, view, "included", "audio rocker should collapse for progressive-only sources")
}

func TestExtractorModel_ShelfRendersAboveDeck(t *testing.T) {
	m := NewExtractorModel("https://youtu.be/aaa", func() {})
	m.width = 120
	m.height = 60

	m = step(t, m,
		ExtractorShelfSeedMsg{URLs: []string{
			"https://youtu.be/aaa",
			"https://youtu.be/bbb",
			"https://youtu.be/ccc",
		}},
		ExtractorShelfMetaMsg{Index: 0, Title: "First Video", Channel: "Chan", Duration: 90 * time.Second, Resolution: "1080p"},
		ExtractorShelfActiveMsg{Index: 0},
		ExtractorShelfStatusMsg{Index: 0, Status: CassettePlaying},
	)
	m = tickN(t, m, 5)

	view := stripANSI(m.View())
	mustContain(t, view, "First Video", "shelf didn't render the active tape title")
	mustContain(t, view, "⏵ 01 / 03", "shelf counter strip missing")
	mustContain(t, view, "HGET·VCR", "deck still rendered below shelf")
}

func TestExtractorModel_ResetDeckClearsBetweenItems(t *testing.T) {
	m := NewExtractorModel("u1", func() {})
	m.width = 100
	m.height = 60

	// Seed first tape, drive it to completion.
	m = step(t, m,
		ExtractorMetaMsg{Title: "Tape One", HasAudio: true, Duration: time.Minute},
		ExtractorPhaseMsg{Phase: "downloading"},
		ExtractorProgressMsg{Percent: 87, Downloaded: 870, Total: 1000, SpeedBPS: 1000},
		ExtractorOutputMsg{Path: "/tmp/Tape One.mp4"},
	)
	m = tickN(t, m, 10)
	view := stripANSI(m.View())
	mustContain(t, view, "Tape One", "tape one didn't render")

	// Reset deck → next tape's metadata should replace it cleanly.
	m = step(t, m,
		ExtractorResetDeckMsg{},
		ExtractorURLMsg{URL: "https://youtu.be/two"},
		ExtractorMetaMsg{Title: "Tape Two", HasAudio: true, Duration: 30 * time.Second},
		ExtractorPhaseMsg{Phase: "downloading"},
		ExtractorProgressMsg{Percent: 5, Downloaded: 50, Total: 1000, SpeedBPS: 500},
	)
	m = tickN(t, m, 10)
	view = stripANSI(m.View())
	mustContain(t, view, "Tape Two", "tape two didn't replace tape one")
	if strings.Contains(view, "Tape One.mp4") {
		t.Errorf("reset failed to clear previous output path:\n%s", view)
	}
	mustContain(t, view, "youtu.be/two", "URL line didn't update for new tape")
}

func TestExtractorModel_ErrorRenders(t *testing.T) {
	m := NewExtractorModel("https://vimeo.com/x", func() {})
	m.width = 100
	m.height = 60
	m = step(t, m,
		ExtractorPhaseMsg{Phase: "downloading"},
		ExtractorErrorMsg{Err: errString("yt-dlp probe failed: oh no")},
	)
	m = tickN(t, m, 5)
	view := m.View()
	mustContain(t, view, "yt-dlp probe failed", "error message missing")
}

// ── helpers ─────────────────────────────────────────────────────────────

type errString string

func (e errString) Error() string { return string(e) }

func mustContain(t *testing.T, view, needle, msg string) {
	t.Helper()
	plain := stripANSI(view)
	if !strings.Contains(plain, needle) {
		t.Errorf("%s: expected to find %q in view\n--- view ---\n%s", msg, needle, plain)
	}
}

// extractPct finds the "NN.N%" text inside the rendered VCR progress row.
// Looks for the line containing "● REC" (the progress strip) and parses
// the percent token at the end of it.
func extractPct(t *testing.T, view string) float64 {
	t.Helper()
	plain := stripANSI(view)
	for _, line := range strings.Split(plain, "\n") {
		if !strings.Contains(line, "● REC") {
			continue
		}
		// Find the rightmost token ending in '%'
		fields := strings.Fields(line)
		for i := len(fields) - 1; i >= 0; i-- {
			tok := strings.TrimRight(fields[i], "%")
			if tok == fields[i] {
				continue
			}
			var f float64
			if _, err := stringsScanf(tok, &f); err == nil {
				return f
			}
		}
	}
	t.Fatalf("no progress row found in view:\n%s", plain)
	return 0
}

// stringsScanf is a tiny float parser that returns (n, err) like fmt.Sscanf
// but tolerates leading whitespace and trailing junk.  Avoids the import
// dance for the only call site above.
func stringsScanf(s string, f *float64) (int, error) {
	s = strings.TrimSpace(s)
	var v float64
	var sign float64 = 1
	if strings.HasPrefix(s, "-") {
		sign = -1
		s = s[1:]
	}
	dot := false
	frac := 0.0
	div := 1.0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			if dot {
				div *= 10
				frac += float64(r-'0') / div
			} else {
				v = v*10 + float64(r-'0')
			}
		case r == '.':
			dot = true
		default:
			goto done
		}
	}
done:
	*f = sign * (v + frac)
	return 1, nil
}

// stripANSI removes ANSI escape sequences from s so test assertions are
// stable regardless of palette / terminal capabilities.
func stripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == 0x1b {
			// Skip until the terminating letter of the CSI sequence.
			i++
			if i < len(s) && s[i] == '[' {
				i++
				for i < len(s) {
					c := s[i]
					i++
					if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
						break
					}
				}
				continue
			}
			// Other escape — skip one char.
			if i < len(s) {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
