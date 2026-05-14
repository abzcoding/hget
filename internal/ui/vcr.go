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
	VCRBrowsing                 // probe complete; user is picking a tape
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

	// Browsing-mode state — populated when the extractor pipeline sends
	// the format table.  All indices are clamped on every accessor so a
	// stale index never reads past the slice.
	videoFormats []ExtractorFormat
	audioFormats []ExtractorFormat
	containers   []string
	videoIdx     int
	audioIdx     int
	containerIdx int
}

// SetFormats seeds the browsing-mode selector with the format table
// emitted by the extractor.  Defaults are highest-quality video, first
// audio track, mp4 container — matching yt-dlp's bv*+ba/b heuristic so
// "just hit enter" produces the pre-selector behaviour.
func (v *VCRAnimation) SetFormats(video, audio []ExtractorFormat, containers []string) {
	v.videoFormats = video
	v.audioFormats = audio
	v.containers = containers
	v.videoIdx = 0
	v.audioIdx = 0
	v.containerIdx = 0
	if len(v.containers) == 0 {
		v.containers = []string{"mp4"}
	}
}

// HasFormats reports whether the browsing UI has anything to show.
func (v VCRAnimation) HasFormats() bool { return len(v.videoFormats) > 0 || len(v.audioFormats) > 0 }

// CycleVideo / CycleAudio / CycleContainer advance (or rewind, with a
// negative step) the corresponding selector by one.  Indices wrap so
// the rocker switch reads as endless rather than bouncing off endstops.
func (v *VCRAnimation) CycleVideo(step int)     { v.videoIdx = cycleIdx(v.videoIdx, step, len(v.videoFormats)) }
func (v *VCRAnimation) CycleAudio(step int)     { v.audioIdx = cycleIdx(v.audioIdx, step, len(v.audioFormats)) }
func (v *VCRAnimation) CycleContainer(step int) { v.containerIdx = cycleIdx(v.containerIdx, step, len(v.containers)) }

func cycleIdx(cur, step, n int) int {
	if n <= 0 {
		return 0
	}
	r := (cur + step) % n
	if r < 0 {
		r += n
	}
	return r
}

// CurrentVideo / CurrentAudio / CurrentContainer return the selected
// rocker positions.  Returned booleans are false when the relevant list
// is empty (e.g. audio-only sources have no separate audio track).
func (v VCRAnimation) CurrentVideo() (ExtractorFormat, bool) {
	if len(v.videoFormats) == 0 {
		return ExtractorFormat{}, false
	}
	return v.videoFormats[clampIdx(v.videoIdx, len(v.videoFormats))], true
}
func (v VCRAnimation) CurrentAudio() (ExtractorFormat, bool) {
	if len(v.audioFormats) == 0 {
		return ExtractorFormat{}, false
	}
	return v.audioFormats[clampIdx(v.audioIdx, len(v.audioFormats))], true
}
func (v VCRAnimation) CurrentContainer() string {
	if len(v.containers) == 0 {
		return "mp4"
	}
	return v.containers[clampIdx(v.containerIdx, len(v.containers))]
}

// Selection builds the full format selection (exact spec + container
// + adaptive descriptors) from the current rocker positions.  When
// the chosen video format is progressive (carries its own audio), the
// audio selector is ignored.  The adaptive descriptors let downstream
// callers translate the pick into a yt-dlp filter expression that
// survives sources lacking the exact format IDs.
func (v VCRAnimation) Selection() ExtractorSelectionMsg {
	sel := ExtractorSelectionMsg{Container: v.CurrentContainer()}
	vf, hasV := v.CurrentVideo()
	if !hasV {
		if af, ok := v.CurrentAudio(); ok {
			sel.Spec = af.ID
			sel.ABRCeiling = int(af.ABR)
			if sel.ABRCeiling == 0 {
				sel.ABRCeiling = int(af.TBR)
			}
		}
		return sel
	}
	// Common video descriptors regardless of progressive vs separate.
	sel.HeightCeiling = vf.Height
	sel.FPSFloor = int(vf.FPS)
	if vf.VCodec != "" && vf.VCodec != "none" {
		sel.VCodec = shortCodec(vf.VCodec)
	}
	if vf.HasAudio || !hasAudioPick(v) {
		// Progressive format, or no audio track to merge in.
		sel.Spec = vf.ID
		sel.Progressive = true
		return sel
	}
	af, _ := v.CurrentAudio()
	sel.Spec = vf.ID + "+" + af.ID
	sel.ABRCeiling = int(af.ABR)
	if sel.ABRCeiling == 0 {
		sel.ABRCeiling = int(af.TBR)
	}
	return sel
}

func hasAudioPick(v VCRAnimation) bool { return len(v.audioFormats) > 0 }

func clampIdx(i, n int) int {
	if n <= 0 {
		return 0
	}
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}

// ExtractorFormat mirrors extractor.Format inside the ui package so the
// renderer doesn't pull a dependency cycle.  Populated via the
// ExtractorFormatsMsg pipeline.
type ExtractorFormat struct {
	ID         string
	Ext        string
	Resolution string
	Height     int
	FPS        float64
	VCodec     string
	ACodec     string
	TBR        float64
	ABR        float64
	Filesize   int64
	Note       string
	HasVideo   bool
	HasAudio   bool
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
	// standby / browsing / eject.  Mod the offset to keep the int small.
	step := 1
	if v.mode != VCRRecording {
		step = 0
		if v.frame%3 == 0 {
			step = 1
		}
	}
	v.tapeOffset = (v.tapeOffset + step) % 4096
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
	// READY LED — solid amber in browsing mode, off otherwise.  This is
	// what visually signals "deck is armed, waiting for you to press
	// REC" without taking a row of text.
	ready := v.mode == VCRBrowsing && (v.frame/16)%2 == 0
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
		chip("READY", ready, Theme.Amber),
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
	recLabel := mag.Render("● REC")
	pctRendered := frost.Render(fmt.Sprintf("%5.1f%%", v.pctSm*100))
	if v.mode == VCRBrowsing {
		// Dim the REC label and show "ARMED" — the deck is loaded but
		// hasn't latched the heads down yet.
		recLabel = fgBoldStyle(Theme.Amber).Render("◐ ARM")
		pctRendered = fgStyle(Theme.Steel).Render("ready")
	}
	progRow := recLabel + "  " + bar + "  " + pctRendered
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
	if v.mode == VCRBrowsing {
		// While browsing: replace the video/audio/rate readouts with
		// three rocker switches the user manipulates in place.  Stable
		// row count keeps the chassis from shifting.
		b.WriteString(chrome.Render("║ ") + pad(v.renderRockerRow("video", v.videoRocker(inner-18)), inner-2) + chrome.Render(" ║") + "\n")
		b.WriteString(chrome.Render("║ ") + pad(v.renderRockerRow("audio", v.audioRocker(inner-18)), inner-2) + chrome.Render(" ║") + "\n")
		b.WriteString(chrome.Render("║ ") + pad(v.renderRockerRow("format", v.containerRocker(inner-18)), inner-2) + chrome.Render(" ║") + "\n")
	} else {
		rowFor("video", v.videoLine(), Theme.Phosphor)
		rowFor("audio", v.audioLine(), Theme.Phosphor)
		rowFor("rate", v.rateLine(), Theme.Mint)
	}

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
	switch v.mode {
	case VCRRecording:
		idx := (v.frame / 4) % 4
		if !left {
			idx = (v.frame / 3) % 4 // take-up spins ~1.3× faster
		}
		return frames[idx]
	case VCRBrowsing:
		// Slow idle-wobble — half-speed, no left/right phase offset.
		// Sells "spinning up" without implying recording.
		return frames[(v.frame/14)%4]
	default:
		return "◯◯◯"
	}
}

// renderTapeStrip draws the magnetic tape itself — a row of glyphs that
// scrolls horizontally.  Filled portion (left of the head) uses dense
// data glyphs; the tail uses spare ones to suggest unrecorded tape.
//
// In browsing mode the strip becomes a slow-shimmer "READY" caption
// flanked by a static texture — the deck is loaded but the heads aren't
// down yet.  Keeps the panel alive without implying progress.
func (v VCRAnimation) renderTapeStrip(width int) string {
	if v.mode == VCRBrowsing {
		return v.renderReadyStrip(width)
	}
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

// renderReadyStrip — browsing-mode tape: gentle horizontal static with
// "READY" stamped in the centre that fades in and out with the frame
// counter.  No progress, no motion bias.
func (v VCRAnimation) renderReadyStrip(width int) string {
	bg := make([]rune, width)
	shades := []rune("·░·░")
	for i := 0; i < width; i++ {
		bg[i] = shades[(i+v.tapeOffset/2)%len(shades)]
	}
	label := " READY "
	pos := (width - len(label)) / 2
	if pos < 0 {
		pos = 0
	}
	for i, r := range label {
		if pos+i < width {
			bg[pos+i] = r
		}
	}
	pulse := (v.frame / 24) % 2
	labelCol := Theme.Amber
	if pulse == 0 {
		labelCol = Theme.Steel
	}
	pre := fgStyle(Theme.Slate).Render(string(bg[:pos]))
	mid := fgBoldStyle(labelCol).Render(string(bg[pos : pos+len(label)]))
	post := fgStyle(Theme.Slate).Render(string(bg[pos+len(label):]))
	return pre + mid + post
}

// renderRockerRow formats one labelled rocker-switch row, matching the
// gutters used by the regular detail-row renderer so the chassis stays
// pixel-stable between browsing and recording.
func (v VCRAnimation) renderRockerRow(label, value string) string {
	steel := fgStyle(Theme.Steel)
	return "  " + steel.Render(rightPad(label, 12)) + value
}

// videoRocker / audioRocker / containerRocker render the three browse
// rockers — left arrow, current selection, right arrow, position chip.
// The arrows pulse subtly while browsing so the affordance reads as
// interactive.  Width is the budget for the value column.
func (v VCRAnimation) videoRocker(width int) string {
	if len(v.videoFormats) == 0 {
		return fgStyle(Theme.Slate).Render("(no video streams)")
	}
	cur, _ := v.CurrentVideo()
	val := formatVideoLine(cur)
	return v.renderRocker(val, v.videoIdx, len(v.videoFormats), width)
}

func (v VCRAnimation) audioRocker(width int) string {
	if !hasAudioPick(v) {
		// Source is progressive-only; the audio rocker collapses into
		// a static "included" caption.
		return fgStyle(Theme.Slate).Render("(included in video stream)")
	}
	if vf, ok := v.CurrentVideo(); ok && vf.HasAudio {
		return fgStyle(Theme.Slate).Render("(progressive — audio bundled)")
	}
	cur, _ := v.CurrentAudio()
	val := formatAudioLine(cur)
	return v.renderRocker(val, v.audioIdx, len(v.audioFormats), width)
}

func (v VCRAnimation) containerRocker(width int) string {
	val := v.CurrentContainer()
	return v.renderRocker(strings.ToUpper(val), v.containerIdx, len(v.containers), width)
}

// renderRocker draws "◀ value ▶  (i/n)" with pulsing arrows.
func (v VCRAnimation) renderRocker(value string, idx, n, width int) string {
	pulse := (v.frame / 10) % 3
	arrowCol := Theme.Magenta
	if pulse == 0 {
		arrowCol = Theme.Slate
	}
	left := fgBoldStyle(arrowCol).Render("◀")
	right := fgBoldStyle(arrowCol).Render("▶")
	pos := fgStyle(Theme.Slate).Render(fmt.Sprintf(" (%d/%d)", idx+1, n))
	valStyled := fgBoldStyle(Theme.Frost).Render(truncate(value, width-12))
	return left + " " + valStyled + " " + right + pos
}

// formatVideoLine / formatAudioLine — compact one-line summaries shown
// on the rocker plate.  We trade exhaustive detail for stable width.
func formatVideoLine(f ExtractorFormat) string {
	parts := []string{}
	switch {
	case f.Note != "":
		parts = append(parts, f.Note)
	case f.Height > 0 && f.FPS > 0:
		parts = append(parts, fmt.Sprintf("%dp%.0f", f.Height, f.FPS))
	case f.Height > 0:
		parts = append(parts, fmt.Sprintf("%dp", f.Height))
	case f.Resolution != "":
		parts = append(parts, f.Resolution)
	}
	if f.VCodec != "" && f.VCodec != "none" {
		parts = append(parts, shortCodec(f.VCodec))
	}
	if f.HasAudio {
		parts = append(parts, "+a")
	}
	if f.Filesize > 0 {
		parts = append(parts, "~"+formatBytes(f.Filesize))
	}
	if f.Ext != "" {
		parts = append(parts, f.Ext)
	}
	return strings.Join(parts, " · ")
}

func formatAudioLine(f ExtractorFormat) string {
	parts := []string{}
	if f.ACodec != "" && f.ACodec != "none" {
		parts = append(parts, shortCodec(f.ACodec))
	}
	if f.ABR > 0 {
		parts = append(parts, fmt.Sprintf("%.0fk", f.ABR))
	} else if f.TBR > 0 {
		parts = append(parts, fmt.Sprintf("%.0fk", f.TBR))
	}
	if f.Filesize > 0 {
		parts = append(parts, "~"+formatBytes(f.Filesize))
	}
	if f.Ext != "" {
		parts = append(parts, f.Ext)
	}
	return strings.Join(parts, " · ")
}

// shortCodec trims yt-dlp's verbose codec strings ("avc1.42001f") down
// to a marketing-friendly form ("avc1") for the narrow rocker plate.
func shortCodec(c string) string {
	if i := strings.IndexAny(c, "."); i > 0 {
		return c[:i]
	}
	return c
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
