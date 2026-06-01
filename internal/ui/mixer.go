package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// mixerWidth — fixed inner width for the mixer panel.  Matches vcrWidth
// so the two panels stack into a coherent rack.
const mixerWidth = 70

// MixerMode is the mixer panel's operational state.
type MixerMode int

const (
	MixerIdle MixerMode = iota
	MixerMixing
	MixerDone
	MixerError
)

// MixerMeta — labels shown on the channel strips and master section.
type MixerMeta struct {
	VideoCodec string // tag for video channel strip
	AudioCodec string // tag for audio channel strip
	Container  string // master section "BUS OUT" caption (mp4 / mkv / webm)
	OutputFile string // shown on the master strip
	Bitrate    string // optional
}

// MixerAnimation — analog-mixer themed visualization for the
// post-processing (ffmpeg mux) phase.  Even though we don't get true
// progress from yt-dlp's internal ffmpeg call, we drive a believable
// rolling animation: VU meters on V/A channel strips, a sweeping EQ
// graph, motorized fader catch-up, and a master meter that pegs as
// the mux finishes.
type MixerAnimation struct {
	mode  MixerMode
	frame int
	meta  MixerMeta

	// Synthetic progress — climbs steadily to 0.95 while mixing, then
	// snaps to 1.0 on MixerDone.  Mux is usually fast enough that real
	// progress wouldn't be more honest than this.
	pct      float64
	finished bool
	startT   time.Time
}

// NewMixer builds a fresh idle mixer animation.
func NewMixer() MixerAnimation {
	return MixerAnimation{mode: MixerIdle, startT: time.Now()}
}

func (m *MixerAnimation) SetMode(mode MixerMode) {
	if mode == MixerMixing && m.mode != MixerMixing {
		m.startT = time.Now()
	}
	if mode == MixerDone {
		m.finished = true
		m.pct = 1.0
	}
	m.mode = mode
}

func (m *MixerAnimation) SetMeta(meta MixerMeta) { m.meta = meta }
func (m *MixerAnimation) Mode() MixerMode        { return m.mode }
func (m *MixerAnimation) Frame() int             { return m.frame }

// Tick advances the animation.  Call once per render frame.
func (m *MixerAnimation) Tick() {
	m.frame++
	if m.finished {
		return
	}
	if m.mode == MixerMixing {
		// Asymptotic approach to 0.95 — ramps up fast, then crawls.
		// Real ffmpeg mux without re-encode finishes in seconds for
		// small videos, minutes for large ones.  This curve "feels"
		// right at both ends.
		dt := time.Since(m.startT).Seconds()
		m.pct = 0.95 * (1 - math.Exp(-dt/4.0))
	}
}

// View renders the mixer panel.
func (m MixerAnimation) View() string {
	chrome := fgStyle(Theme.Phosphor)
	frame := fgStyle(Theme.Slate)
	steel := fgStyle(Theme.Steel)
	frost := fgBoldStyle(Theme.Frost)
	amber := fgBoldStyle(Theme.Amber)
	mint := fgBoldStyle(Theme.Mint)

	inner := mixerWidth - 2
	pad := func(s string, w int) string {
		gap := w - lipgloss.Width(s)
		if gap < 0 {
			gap = 0
		}
		return s + strings.Repeat(" ", gap)
	}
	centre := func(s string, w int) string {
		gap := w - lipgloss.Width(s)
		if gap < 0 {
			gap = 0
		}
		l := gap / 2
		r := gap - l
		return strings.Repeat(" ", l) + s + strings.Repeat(" ", r)
	}

	var b strings.Builder

	// ── Top bezel. ──────────────────────────────────────────────────────
	b.WriteString(chrome.Render("╔") +
		chrome.Render(strings.Repeat("═", inner)) +
		chrome.Render("╗") + "\n")

	// ── Brand plate. ────────────────────────────────────────────────────
	plate := amber.Render("▓▓ HGET·MIX·M-808 ▓▓") +
		steel.Render("  8-BUS ANALOG  ") +
		frost.Render("+4 dBu")
	b.WriteString(chrome.Render("║") + centre(plate, inner) + chrome.Render("║") + "\n")

	// ── Divider. ────────────────────────────────────────────────────────
	b.WriteString(chrome.Render("╠") +
		frame.Render(strings.Repeat("═", inner)) +
		chrome.Render("╣") + "\n")

	// ── Status caption with mode-driven LEDs. ───────────────────────────
	pwr := true
	mix := m.mode == MixerMixing && (m.frame/8)%2 == 0
	clip := m.mode == MixerMixing && (m.frame/30)%5 == 0 // occasional clip blink
	bus := m.mode == MixerMixing
	sync := m.mode == MixerDone

	chip := func(name string, on bool, col lipgloss.Color) string {
		c := Theme.Slate
		if on {
			c = col
		}
		return fgBoldStyle(c).Render("◉") + " " + steel.Render(name)
	}
	chips := strings.Join([]string{
		chip("PWR", pwr, Theme.Mint),
		chip("MIX", mix, Theme.Amber),
		chip("BUS", bus, Theme.Phosphor),
		chip("CLIP", clip, Theme.Magenta),
		chip("LOCK", sync, Theme.Mint),
	}, "  ")
	b.WriteString(chrome.Render("║ ") + pad("[ "+steel.Render("CONSOLE")+" ]  "+chips, inner-2) + chrome.Render(" ║") + "\n")

	// ── EQ window framing (top). ────────────────────────────────────────
	b.WriteString(chrome.Render("║ ") +
		frame.Render("┌"+strings.Repeat("─", inner-4)+"┐") +
		chrome.Render(" ║") + "\n")

	// ── Spectrum analyser (8-band rolling EQ). ──────────────────────────
	for row := 0; row < 5; row++ {
		line := m.renderSpectrumRow(row, inner-6)
		b.WriteString(chrome.Render("║ ") +
			frame.Render("│") + " " + pad(line, inner-6) + " " +
			frame.Render("│") +
			chrome.Render(" ║") + "\n")
	}
	// Frequency axis labels under the spectrum.
	axis := steel.Render("60   125   250   500   1k    2k    4k    8k    16k")
	b.WriteString(chrome.Render("║ ") +
		frame.Render("│") + " " + pad(axis, inner-6) + " " +
		frame.Render("│") +
		chrome.Render(" ║") + "\n")

	// ── EQ window framing (bottom). ─────────────────────────────────────
	b.WriteString(chrome.Render("║ ") +
		frame.Render("└"+strings.Repeat("─", inner-4)+"┘") +
		chrome.Render(" ║") + "\n")

	// ── Channel strip header. ───────────────────────────────────────────
	hdr := steel.Render("  CH 1 ") + amber.Render("VIDEO") +
		strings.Repeat(" ", 8) +
		steel.Render("CH 2 ") + amber.Render("AUDIO") +
		strings.Repeat(" ", 8) +
		steel.Render("MASTER ") + mint.Render("BUS OUT")
	b.WriteString(chrome.Render("║ ") + pad(hdr, inner-2) + chrome.Render(" ║") + "\n")

	// ── Three vertical fader strips drawn as one row of segmented bars.
	for row := 0; row < 6; row++ {
		line := m.renderFaderRow(row)
		b.WriteString(chrome.Render("║ ") + pad(line, inner-2) + chrome.Render(" ║") + "\n")
	}

	// ── Channel labels under the faders. ────────────────────────────────
	labels := steel.Render("  ") + steel.Render(rightPad(short(m.meta.VideoCodec, 12), 14)) +
		steel.Render(rightPad(short(m.meta.AudioCodec, 12), 14)) +
		steel.Render(rightPad(short(strings.ToUpper(m.meta.Container), 12), 14))
	b.WriteString(chrome.Render("║ ") + pad(labels, inner-2) + chrome.Render(" ║") + "\n")

	// ── Patch bay row — animated cable connections. ─────────────────────
	bay := m.renderPatchBay(inner - 2)
	b.WriteString(chrome.Render("║ ") + pad(bay, inner-2) + chrome.Render(" ║") + "\n")

	// ── Output detail rows. ─────────────────────────────────────────────
	rowFor := func(label, val string, valCol lipgloss.Color) {
		if val == "" {
			val = "—"
		}
		l := steel.Render(rightPad(label, 12))
		valSty := fgStyle(valCol)
		if valCol == Theme.Frost {
			valSty = frost
		}
		ln := "  " + l + valSty.Render(truncate(val, inner-18))
		b.WriteString(chrome.Render("║ ") + pad(ln, inner-2) + chrome.Render(" ║") + "\n")
	}
	rowFor("output", m.meta.OutputFile, Theme.Frost)
	rowFor("container", strings.ToUpper(m.meta.Container), Theme.Phosphor)
	rowFor("status", m.statusLine(), m.statusColor())

	// ── Bottom bezel. ───────────────────────────────────────────────────
	b.WriteString(chrome.Render("╚") +
		chrome.Render(strings.Repeat("═", inner)) +
		chrome.Render("╝"))

	return b.String()
}

// renderSpectrumRow draws one row of the 8-band spectrum analyser.  Bars
// fill from the bottom up (row 4 is bottom).  Heights are deterministic
// pseudo-random per band per frame so the analyser looks lively but
// reproducible (handy for snapshot tests).
func (m MixerAnimation) renderSpectrumRow(row, width int) string {
	const bands = 9 // 60 .. 16k
	bandWidth := width / bands
	if bandWidth < 4 {
		bandWidth = 4
	}
	var sb strings.Builder
	for i := 0; i < bands; i++ {
		h := m.bandHeight(i)
		// row 0 = top, row 4 = bottom.  Bar is "lit" from bottom up.
		filled := h >= (5 - row)
		var glyph string
		var col lipgloss.Color
		switch {
		case row == 0 && filled:
			glyph, col = "▀", Theme.Magenta
		case row <= 1 && filled:
			glyph, col = "█", Theme.Amber
		case filled:
			glyph, col = "█", Theme.Mint
		default:
			glyph, col = " ", Theme.Slate
		}
		bar := fgBoldStyle(col).Render(strings.Repeat(glyph, 3))
		gap := strings.Repeat(" ", bandWidth-3)
		sb.WriteString(bar + gap)
	}
	return sb.String()
}

// bandHeight returns 0..5 for the i-th spectrum band at the current
// frame.  Uses a sine + per-band offset for a pleasing interleaved roll
// when mixing; flat-zero otherwise.
func (m MixerAnimation) bandHeight(band int) int {
	if m.mode != MixerMixing {
		return 0
	}
	t := float64(m.frame) / 8.0
	phase := float64(band) * 0.7
	v := 0.5 + 0.5*math.Sin(t+phase) + 0.2*math.Sin(t*1.7+phase*2)
	// add a touch of low-band weighting (musical heuristic — bass heavier)
	weight := 1.0 - float64(band)/12.0
	v *= weight
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	return int(v * 5)
}

// renderFaderRow draws one row across the three channel strips.  Six
// rows total give us a tall enough fader to show segmented levels.
func (m MixerAnimation) renderFaderRow(row int) string {
	steel := fgStyle(Theme.Steel)
	const rows = 6
	// Per-channel level (0..rows).  Channels 1 + 2 oscillate, master
	// climbs with pct.
	levelV := m.channelLevel(0)
	levelA := m.channelLevel(1)
	levelM := int(m.pct*float64(rows)*0.95 + 0.5)
	if m.mode == MixerDone {
		levelM = rows
	}

	cell := func(level int) string {
		// row 0 = top of fader, row 5 = bottom.  fader cap (motor knob)
		// rides at row = rows - level - 1.
		capRow := rows - level - 1
		if capRow < 0 {
			capRow = 0
		}
		switch {
		case row == capRow:
			return fgBoldStyle(Theme.Frost).Render("▭")
		case row > capRow:
			// Filled below cap.  Color shifts amber → magenta near top.
			col := Theme.Mint
			if row < rows/2 {
				col = Theme.Amber
			}
			if row == 0 {
				col = Theme.Magenta
			}
			return fgBoldStyle(col).Render("▮")
		default:
			return steel.Render("│")
		}
	}

	// Each strip: " ╎ <cell> ╎ " spaced like a slot.
	strip := func(level int) string {
		return steel.Render(" ╎ ") + cell(level) + steel.Render(" ╎ ")
	}
	gap := strings.Repeat(" ", 6)
	return "  " + strip(levelV) + gap + strip(levelA) + gap + strip(levelM)
}

// channelLevel returns 0..5 for input channel `ch` at the current frame.
// Audio channel runs hotter than video for personality.
func (m MixerAnimation) channelLevel(ch int) int {
	if m.mode == MixerDone {
		return 5
	}
	if m.mode != MixerMixing {
		return 0
	}
	t := float64(m.frame) / 6.0
	base := 0.55 + 0.3*math.Sin(t+float64(ch)*1.7)
	if ch == 1 {
		base += 0.1 // audio hotter
	}
	if base < 0 {
		base = 0
	}
	if base > 1 {
		base = 1
	}
	return int(base * 5)
}

// renderPatchBay draws the iconic 1/4" patch bay along the bottom — a
// row of jacks with cables looping between V, A, and master.
func (m MixerAnimation) renderPatchBay(width int) string {
	steel := fgStyle(Theme.Steel)
	mint := fgBoldStyle(Theme.Mint)
	amber := fgBoldStyle(Theme.Amber)

	// 12 jacks across the bay.
	jacks := []string{}
	for i := 0; i < 12; i++ {
		switch {
		case i == 1 || i == 2:
			jacks = append(jacks, amber.Render("⊙"))
		case i == 5 || i == 6:
			jacks = append(jacks, amber.Render("⊙"))
		case i == 9 || i == 10:
			jacks = append(jacks, mint.Render("⊙"))
		default:
			jacks = append(jacks, steel.Render("◌"))
		}
	}
	cable := mint.Render("─")
	if m.mode == MixerMixing && (m.frame/4)%2 == 0 {
		cable = amber.Render("─")
	}
	loop := steel.Render(" PATCH ") + strings.Join(jacks, cable)
	return loop
}

func (m MixerAnimation) statusLine() string {
	switch m.mode {
	case MixerIdle:
		return "standby — awaiting bus assignment"
	case MixerMixing:
		dots := strings.Repeat(".", (m.frame/8)%4)
		return fmt.Sprintf("muxing video + audio streams%s", dots)
	case MixerDone:
		return "BUS LOCKED · master at unity · output flushed"
	case MixerError:
		return "fault — see log"
	}
	return ""
}

func (m MixerAnimation) statusColor() lipgloss.Color {
	switch m.mode {
	case MixerDone:
		return Theme.Mint
	case MixerError:
		return Theme.Magenta
	default:
		return Theme.Amber
	}
}

func short(s string, n int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "—"
	}
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
