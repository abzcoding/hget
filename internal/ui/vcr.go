package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/harmonica"
	"github.com/charmbracelet/lipgloss"
)

// vcrWidth — the VCR panel renders at a fixed inner width so the
// front-plate, tape window, and counter stay aligned regardless of the
// actual terminal width.  Wrapper code centres the panel.
const vcrWidth = 70

// VCRMode is the operational state of the VCR panel.  Mirrors the
// extractor.Phase but stays in the ui package so the renderer never
// imports extractor.
type VCRMode int

const (
	VCRStandby   VCRMode = iota // pre-recording, "tape inserted" state
	VCRRecording                // [download] phase active
	VCREjecting                 // post-record cooldown (between phases)
	VCRError
)

// VCRMeta — the metadata strip shown above the tape window.  All fields
// are optional; absent values render as "—".
type VCRMeta struct {
	Title       string
	Channel     string
	Duration    time.Duration
	Resolution  string
	FPS         float64
	VCodec      string
	ACodec      string
	Container   string
	HasAudio    bool   // controls whether the AUDIO meter is shown
	StreamLabel string // "VIDEO" / "AUDIO" / "" — drives front-LED
}

// VCRAnimation drives the on-screen VCR.  Tick() advances the animation
// frame; Update() pushes new download progress.  The renderer is pure.
type VCRAnimation struct {
	mode  VCRMode
	frame int
	width int

	// Download stats (live)
	pct          float64
	downloaded   int64
	total        int64
	speed        float64
	eta          time.Duration
	fragment     int
	fragmentN    int

	// Spring-smoothed percentage and reel rotation.  We drive both the
	// progress bar and the spinning reel glyphs from spring output so
	// the visual continuity matches the data.
	bar     progress.Model
	pctSpr  harmonica.Spring
	pctSm   float64
	pctVel  float64
	pctTgt  float64

	// Visible meta (header strip + transport caption).
	meta VCRMeta

	// Tape window — a horizontal strip of glyphs that scrolls right as
	// the download progresses.  We keep an internal "head position"
	// that advances with elapsed time so the moving texture is decoupled
	// from progress (you still see motion when buffering / paused).
	tapeOffset int
	tapeStartT time.Time
}

// NewVCR builds a fresh, idle VCR animation.
func NewVCR() VCRAnimation {
	return VCRAnimation{
		mode: VCRStandby,
		bar: progress.New(
			progress.WithSolidFill(string(Theme.Magenta)),
			progress.WithoutPercentage(),
			progress.WithWidth(vcrWidth-26),
		),
		pctSpr:     harmonica.NewSpring(harmonica.FPS(60), 6.0, 0.85),
		tapeStartT: time.Now(),
		width:      vcrWidth,
	}
}

func (v *VCRAnimation) SetMode(m VCRMode)    { v.mode = m }
func (v *VCRAnimation) SetMeta(m VCRMeta)    { v.meta = m }
func (v *VCRAnimation) Mode() VCRMode        { return v.mode }
func (v *VCRAnimation) Frame() int           { return v.frame }

// Update pushes new download stats.  Called whenever the extractor emits
// a DownloadProgress event.
func (v *VCRAnimation) Update(pct float64, downloaded, total int64, speedBPS float64, eta time.Duration, fragment, fragmentN int) {
	v.pct = pct / 100.0
	if v.pct < 0 {
		v.pct = 0
	}
	if v.pct > 1 {
		v.pct = 1
	}
	v.downloaded = downloaded
	v.total = total
	v.speed = speedBPS
	v.eta = eta
	v.fragment = fragment
	v.fragmentN = fragmentN
	v.pctTgt = v.pct
}

// Tick advances the animation.  Call once per render frame (~60 Hz).
func (v *VCRAnimation) Tick() {
	v.frame++
	v.pctSm, v.pctVel = v.pctSpr.Update(v.pctSm, v.pctVel, v.pctTgt)
	if v.pctSm < 0 {
		v.pctSm = 0
	}
	if v.pctSm > 1 {
		v.pctSm = 1
	}
	// Tape head scrolls at a fixed rate while recording, slower during
	// standby / eject.  Mod the offset to keep the int small.
	step := 1
	if v.mode != VCRRecording {
		step = 0
		if v.frame%3 == 0 {
			step = 1
		}
	}
	v.tapeOffset = (v.tapeOffset + step) % 1024
}

// View renders the entire VCR panel as a single string.
func (v VCRAnimation) View() string {
	chrome := fgStyle(Theme.Phosphor)
	frame := fgStyle(Theme.Slate)
	steel := fgStyle(Theme.Steel)
	frost := fgBoldStyle(Theme.Frost)
	mag := fgBoldStyle(Theme.Magenta) // "REC" red — VCR signature colour

	inner := v.width - 2
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
	plate := mag.Render("▓▓ HGET·VCR/4-HEAD ▓▓") +
		steel.Render("  HI-FI STEREO  ") +
		frost.Render("NTSC/PAL")
	b.WriteString(chrome.Render("║") + centre(plate, inner) + chrome.Render("║") + "\n")

	// ── Divider. ────────────────────────────────────────────────────────
	b.WriteString(chrome.Render("╠") +
		frame.Render(strings.Repeat("═", inner)) +
		chrome.Render("╣") + "\n")

	// ── Transport LEDs. ─────────────────────────────────────────────────
	pwr := true
	rec := v.mode == VCRRecording && (v.frame/8)%2 == 0 // pulsing red REC
	tape := v.mode == VCRRecording
	stereo := v.meta.HasAudio
	hifi := v.meta.HasAudio && v.mode == VCRRecording && (v.frame/12)%2 == 0
	chip := func(name string, on bool, col lipgloss.Color) string {
		c := Theme.Slate
		if on {
			c = col
		}
		return fgBoldStyle(c).Render("◉") + " " + steel.Render(name)
	}
	chips := strings.Join([]string{
		chip("PWR", pwr, Theme.Mint),
		chip("REC", rec, Theme.Magenta),
		chip("TAPE", tape, Theme.Phosphor),
		chip("STEREO", stereo, Theme.Amber),
		chip("HI-FI", hifi, Theme.Phosphor),
	}, "  ")
	b.WriteString(chrome.Render("║ ") + pad("[ "+steel.Render("TRANSPORT")+" ]  "+chips, inner-2) + chrome.Render(" ║") + "\n")

	// ── Tape window framing (top). ──────────────────────────────────────
	b.WriteString(chrome.Render("║ ") +
		frame.Render("┌"+strings.Repeat("─", inner-4)+"┐") +
		chrome.Render(" ║") + "\n")

	// ── Reels + tape strip. ─────────────────────────────────────────────
	leftReel := v.reelGlyph(true)
	rightReel := v.reelGlyph(false)
	stripWidth := inner - 12 // space for two reels (3 wide each) + padding
	if stripWidth < 8 {
		stripWidth = 8
	}
	tapeStrip := v.renderTapeStrip(stripWidth)
	reelStyled := fgBoldStyle(Theme.Frost)
	row := reelStyled.Render(leftReel) + " " + tapeStrip + " " + reelStyled.Render(rightReel)
	b.WriteString(chrome.Render("║ ") +
		frame.Render("│") + " " +
		pad(row, inner-6) + " " +
		frame.Render("│") +
		chrome.Render(" ║") + "\n")

	// ── Counter strip (HH:MM:SS / total + fragment counter). ────────────
	counter := v.renderCounter()
	b.WriteString(chrome.Render("║ ") +
		frame.Render("│") + " " +
		pad(counter, inner-6) + " " +
		frame.Render("│") +
		chrome.Render(" ║") + "\n")

	// ── Tape window framing (bottom). ───────────────────────────────────
	b.WriteString(chrome.Render("║ ") +
		frame.Render("└"+strings.Repeat("─", inner-4)+"┘") +
		chrome.Render(" ║") + "\n")

	// ── Progress bar + percent. ─────────────────────────────────────────
	bar := v.bar.ViewAs(v.pctSm)
	pctTxt := fmt.Sprintf("%5.1f%%", v.pctSm*100)
	progRow := mag.Render("● REC") + "  " + bar + "  " + frost.Render(pctTxt)
	b.WriteString(chrome.Render("║ ") + pad(progRow, inner-2) + chrome.Render(" ║") + "\n")

	// ── Audio VU meters (peak-style bars driven by speed). ──────────────
	vuL, vuR := v.renderVU()
	vuRow := steel.Render("AUDIO  L ") + vuL + "   " + steel.Render("R ") + vuR
	b.WriteString(chrome.Render("║ ") + pad(vuRow, inner-2) + chrome.Render(" ║") + "\n")

	// ── Detail rows (always present, stable height). ────────────────────
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
	rowFor("title", v.meta.Title, Theme.Frost)
	rowFor("channel", v.meta.Channel, Theme.Amber)
	rowFor("video", v.videoLine(), Theme.Phosphor)
	rowFor("audio", v.audioLine(), Theme.Phosphor)
	rowFor("rate", v.rateLine(), Theme.Mint)

	// ── Bottom rivet plate. ─────────────────────────────────────────────
	rivetCount := (inner - 2) / 2
	rivets := strings.Repeat(steel.Render("▪")+" ", rivetCount)
	b.WriteString(chrome.Render("║ ") + pad(rivets, inner-2) + chrome.Render(" ║") + "\n")

	// ── Bottom bezel with cassette slot. ────────────────────────────────
	half := (inner - 5) / 2
	b.WriteString(chrome.Render("╚") +
		chrome.Render(strings.Repeat("═", half)) +
		chrome.Render("┤▒▒▒├") +
		chrome.Render(strings.Repeat("═", inner-half-5)) +
		chrome.Render("╝"))

	return b.String()
}

// reelGlyph picks one of four rotating-reel frames.  When recording, the
// take-up reel (right) spins faster than the supply reel (left) — a tiny
// detail that sells the illusion.
func (v VCRAnimation) reelGlyph(left bool) string {
	frames := []string{"◜◠◝", "◝◠◞", "◞◡◟", "◟◡◜"}
	if v.mode != VCRRecording {
		return "◯◯◯"
	}
	idx := (v.frame / 4) % 4
	if !left {
		idx = (v.frame / 3) % 4 // take-up spins ~1.3× faster
	}
	return frames[idx]
}

// renderTapeStrip draws the magnetic tape itself — a row of glyphs that
// scrolls horizontally.  Filled portion (left of the head) uses dense
// data glyphs; the tail uses spare ones to suggest unrecorded tape.
func (v VCRAnimation) renderTapeStrip(width int) string {
	headPos := int(v.pctSm * float64(width))
	if headPos > width {
		headPos = width
	}
	dense := []rune("▓▒░▒")
	sparse := []rune("·  ·")
	out := make([]rune, 0, width)
	for i := 0; i < width; i++ {
		var r rune
		if i < headPos {
			r = dense[(i+v.tapeOffset)%len(dense)]
		} else {
			r = sparse[(i+v.tapeOffset)%len(sparse)]
		}
		out = append(out, r)
	}
	recorded := fgStyle(Theme.Magenta).Render(string(out[:headPos]))
	upcoming := fgStyle(Theme.Slate).Render(string(out[headPos:]))
	return recorded + upcoming
}

// renderCounter renders the classic four-digit VCR counter — but driven
// by elapsed download time, plus the fragment readout when applicable.
func (v VCRAnimation) renderCounter() string {
	steel := fgStyle(Theme.Steel)
	amber := fgBoldStyle(Theme.Amber)

	elapsed := time.Duration(0)
	if v.total > 0 && v.speed > 0 {
		// derive elapsed from downloaded / speed — stable readout when
		// process isn't tracking real wall time
		elapsed = time.Duration(float64(v.downloaded) / v.speed * float64(time.Second))
	}
	cnt := amber.Render(fmt.Sprintf("%s", formatHMS(elapsed)))
	totalTxt := steel.Render(" / ") + amber.Render(formatHMS(v.meta.Duration))
	if v.meta.Duration == 0 {
		totalTxt = steel.Render(" / ") + steel.Render("--:--:--")
	}

	frag := ""
	if v.fragmentN > 0 {
		frag = "   " + steel.Render("FRAG ") +
			amber.Render(fmt.Sprintf("%04d", v.fragment)) +
			steel.Render("/") +
			amber.Render(fmt.Sprintf("%04d", v.fragmentN))
	}
	eta := ""
	if v.eta > 0 {
		eta = "   " + steel.Render("ETA ") + amber.Render(formatHMS(v.eta))
	}
	return steel.Render("CTR ") + cnt + totalTxt + frag + eta
}

// renderVU draws two stereo VU meter bars whose level fluctuates around
// the current download speed.  We add a tiny per-frame jitter so the
// meter "breathes" like an analog needle rather than locking flat.
func (v VCRAnimation) renderVU() (string, string) {
	const segs = 14
	// Map speed to 0..1; cap at 5 MiB/s as the "0 dB" line.
	level := v.speed / (5 * 1024 * 1024)
	if level > 1 {
		level = 1
	}
	if v.mode != VCRRecording {
		level = 0
	}
	jitterL := float64((v.frame*7)%17) / 64.0
	jitterR := float64((v.frame*11)%19) / 64.0
	lvlL := clamp01(level + jitterL - 0.07)
	lvlR := clamp01(level + jitterR - 0.07)
	return vuBar(lvlL, segs), vuBar(lvlR, segs)
}

func vuBar(level float64, segs int) string {
	on := int(level * float64(segs))
	var sb strings.Builder
	for i := 0; i < segs; i++ {
		if i < on {
			switch {
			case i >= segs*4/5:
				sb.WriteString(fgBoldStyle(Theme.Magenta).Render("▮")) // peak / clip
			case i >= segs*3/5:
				sb.WriteString(fgBoldStyle(Theme.Amber).Render("▮"))
			default:
				sb.WriteString(fgBoldStyle(Theme.Mint).Render("▮"))
			}
		} else {
			sb.WriteString(fgStyle(Theme.Slate).Render("▯"))
		}
	}
	return sb.String()
}

func (v VCRAnimation) videoLine() string {
	parts := []string{}
	if v.meta.Resolution != "" {
		parts = append(parts, v.meta.Resolution)
	}
	if v.meta.FPS > 0 {
		parts = append(parts, fmt.Sprintf("%.0ffps", v.meta.FPS))
	}
	if v.meta.VCodec != "" && v.meta.VCodec != "none" {
		parts = append(parts, v.meta.VCodec)
	}
	return strings.Join(parts, " · ")
}

func (v VCRAnimation) audioLine() string {
	if !v.meta.HasAudio {
		return "muted / no separate audio stream"
	}
	parts := []string{}
	if v.meta.ACodec != "" && v.meta.ACodec != "none" {
		parts = append(parts, v.meta.ACodec)
	}
	parts = append(parts, "stereo · 48 kHz")
	return strings.Join(parts, " · ")
}

func (v VCRAnimation) rateLine() string {
	if v.speed <= 0 {
		return "—"
	}
	parts := []string{
		fmt.Sprintf("%s/s", formatBytes(int64(v.speed))),
		fmt.Sprintf("%s of %s", formatBytes(v.downloaded), formatBytes(v.total)),
	}
	return strings.Join(parts, "   ·   ")
}

// ── helpers ────────────────────────────────────────────────────────────

func clamp01(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

func rightPad(s string, w int) string {
	if lipgloss.Width(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-lipgloss.Width(s))
}

func formatHMS(d time.Duration) string {
	if d <= 0 {
		return "00:00:00"
	}
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}
