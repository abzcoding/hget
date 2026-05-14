package ui

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// tapeState matches mainframeState semantics for the tape side of the bus.
type tapeState int

const (
	tapeIdle         tapeState = iota
	tapeMounting               // pre-transfer; reels not yet spinning
	tapeTransferring           // reels rotating, fill bar climbing
	tapeComplete               // green LEDs, reels stopped
	tapeDisconnected           // red blinking LEDs, frozen reels
)

// reelHubFrames cycles through a phosphor reel rotation.  Four glyphs ⇒
// one rotation per 4 frames at full speed; throttled by velocity below.
var reelHubFrames = []string{"◐", "◓", "◑", "◒"}

// tape models a single tape unit.
type tape struct {
	frame    int
	state    tapeState
	progress float64 // 0..1 — drives fill ribbon
	speedBps float64
	peakBps  float64
	label    string
}

func newTape(label string) tape {
	return tape{label: label}
}

func (t *tape) SetState(s tapeState) { t.state = s }

func (t *tape) Update(progress, speedBps, peakBps float64) {
	if progress < 0 {
		progress = 0
	}
	if progress > 1 {
		progress = 1
	}
	t.progress = progress
	t.speedBps = speedBps
	t.peakBps = peakBps
}

func (t *tape) Tick() { t.frame++ }

func (t tape) hubGlyph(invert bool) string {
	switch t.state {
	case tapeDisconnected, tapeIdle:
		return "◯"
	case tapeComplete:
		return "◉"
	case tapeMounting:
		idx := (t.frame / 6) % len(reelHubFrames)
		if invert {
			idx = (len(reelHubFrames) - 1 - idx + len(reelHubFrames)) % len(reelHubFrames)
		}
		return reelHubFrames[idx]
	}
	// transferring — speed-scaled
	ratio := 0.0
	if t.peakBps > 0 {
		ratio = math.Min(t.speedBps/t.peakBps, 1.0)
	}
	step := 6 - int(5*ratio)
	if step < 1 {
		step = 1
	}
	idx := (t.frame / step) % len(reelHubFrames)
	if invert {
		idx = (len(reelHubFrames) - 1 - idx + len(reelHubFrames)) % len(reelHubFrames)
	}
	return reelHubFrames[idx]
}

func (t tape) blink() bool { return (t.frame/8)%2 == 0 }

// ── Banner mode ───────────────────────────────────────────────────────────────

const tapeBannerWidth = dataLinkInnerW + 2 // 72

// renderReel draws a 7×5 reel block: a hexagonal flange with quarter-arcs
// at the corners and an animated hub in the centre.  Returns 5 lines.
func (t tape) renderReel(invert bool, accent lipgloss.Color) []string {
	flange := fgStyle(colorSteel)
	hub := fgBoldStyle(accent)
	rim := fgStyle(accent)

	hubGlyph := hub.Render(t.hubGlyph(invert))

	r0 := flange.Render("╭─────╮")
	r1 := flange.Render("│") + " " +
		rim.Render("◜") + rim.Render("─") + rim.Render("◝") +
		" " + flange.Render("│")
	r2 := flange.Render("│") + " " +
		rim.Render("│") + hubGlyph + rim.Render("│") +
		" " + flange.Render("│")
	r3 := flange.Render("│") + " " +
		rim.Render("◟") + rim.Render("─") + rim.Render("◞") +
		" " + flange.Render("│")
	r4 := flange.Render("╰─────╯")
	return []string{r0, r1, r2, r3, r4}
}

func (t tape) ViewBanner() string {
	chrome := fgStyle(colorPhosphor)
	frame := fgStyle(colorSlate)
	steel := fgStyle(colorSteel)
	frost := fgBoldStyle(colorFrost)
	amber := fgBoldStyle(colorAmber)
	mint := fgBoldStyle(colorMint)
	mag := fgBoldStyle(colorMagenta)

	// Pick palette per state.
	var reelCol, fillCol, dimCol lipgloss.Color
	dimCol = colorSlate
	statusTxt := "READY"
	statusSty := steel
	switch t.state {
	case tapeIdle:
		reelCol, fillCol = colorSteel, colorSlate
		statusTxt, statusSty = "MOUNT", steel
	case tapeMounting:
		reelCol, fillCol = colorAmber, colorAmber
		statusTxt, statusSty = "ARMING", amber
	case tapeTransferring:
		reelCol, fillCol = colorPhosphor, colorPhosphor
		statusTxt, statusSty = "RECORDING", amber
	case tapeComplete:
		reelCol, fillCol = colorMint, colorMint
		statusTxt, statusSty = "ARCHIVED", mint
	case tapeDisconnected:
		reelCol, fillCol = colorMagenta, colorMagenta
		if t.blink() {
			statusTxt = "▲ LINK LOST"
		} else {
			statusTxt = "  LINK LOST"
		}
		statusSty = mag
	}

	innerW := tapeBannerWidth - 2

	pad := func(s string, w int) string {
		gap := w - lipgloss.Width(s)
		if gap < 0 {
			gap = 0
		}
		return s + strings.Repeat(" ", gap)
	}

	// ── Row 0: top with label and status pill. ───────────────────────────
	labelTxt := fmt.Sprintf("[ %s ]", t.label)
	pctTxt := fmt.Sprintf("[ %s %.0f%% ]", statusTxt, t.progress*100)
	if t.state == tapeIdle || t.state == tapeMounting {
		pctTxt = fmt.Sprintf("[ %s ]", statusTxt)
	}
	labelW := lipgloss.Width(labelTxt)
	pillW := lipgloss.Width(pctTxt)
	dashLeft := 2
	dashRight := 2
	dashesAvail := innerW - dashLeft - labelW - pillW - dashRight
	if dashesAvail < 2 {
		dashesAvail = 2
	}
	row0 := chrome.Render("╭") +
		frame.Render(strings.Repeat("─", dashLeft)) +
		chrome.Render(labelTxt) +
		frame.Render(strings.Repeat("─", dashesAvail)) +
		statusSty.Render(pctTxt) +
		frame.Render(strings.Repeat("─", dashRight)) +
		chrome.Render("╮")

	// ── Rows 1–7: chassis interior + reels + ribbon. ─────────────────────
	// Geometry inside the box (innerW=70):
	//   " " + reelL(7) + ribbon(54) + reelR(7) + " " = 70.
	const (
		sidePad = 1
		reelW   = 7
	)
	ribbonW := innerW - 2*sidePad - 2*reelW // 54

	leftReel := t.renderReel(false, reelCol)
	rightReel := t.renderReel(true, reelCol)

	// Tape ribbon: 5 rows tall, ribbonW wide.  Top + bottom rows are the
	// recorded-tape "edges"; the middle row is the magnetic strip with a
	// fill bar; the second/fourth rows show light shimmer when active.
	ribbonRows := t.renderRibbon(ribbonW, fillCol, dimCol)

	// 5 reel rows, but tape ribbon is also 5 rows — perfect alignment.
	innerRows := make([]string, 5)
	for i := 0; i < 5; i++ {
		row := strings.Repeat(" ", sidePad) +
			leftReel[i] +
			ribbonRows[i] +
			rightReel[i] +
			strings.Repeat(" ", sidePad)
		innerRows[i] = chrome.Render("│") + pad(row, innerW) + chrome.Render("│")
	}

	// ── Row 1 (above the reels): rivet rail. ─────────────────────────────
	rivet := steel.Render("·")
	rivetRow := strings.Repeat(rivet+" ", innerW/2)
	row1 := chrome.Render("│") + pad(rivetRow, innerW) + chrome.Render("│")

	// ── Row 7: SUPPLY / TAKE-UP labels + telemetry. ──────────────────────
	supplyLbl := steel.Render("SUPPLY ")
	takeLbl := steel.Render(" TAKE-UP")
	pctSty := frost.Render(fmt.Sprintf("%5.1f%%", t.progress*100))
	var spd string
	switch {
	case t.state == tapeComplete:
		spd = mint.Render("· tape sealed")
	case t.state == tapeDisconnected:
		spd = mag.Render("· connection severed")
	case t.speedBps > 0:
		spd = amber.Render("· ↓ "+formatBytes(int64(t.speedBps))) + steel.Render("/s")
	default:
		spd = steel.Render("· awaiting carrier")
	}
	mid := steel.Render(" spool ") + pctSty + " " + spd
	row7content := " " + supplyLbl + mid + takeLbl
	row7 := chrome.Render("│") + pad(row7content, innerW) + chrome.Render("│")

	// ── Row 8: bottom border that closes the chassis cleanly. ────────────
	row8 := chrome.Render("╰" + strings.Repeat("─", innerW) + "╯")

	// Assemble.  Sequence: top, rivet rail, 5 reel/ribbon rows, telemetry,
	// bottom rule.
	rows := []string{row0, row1}
	rows = append(rows, innerRows...)
	rows = append(rows, row7, row8)
	return strings.Join(rows, "\n")
}

func (t tape) renderRibbon(w int, fillCol, dimCol lipgloss.Color) []string {
	if w < 4 {
		w = 4
	}
	on := fgBoldStyle(fillCol)
	off := fgStyle(dimCol)
	amber := fgBoldStyle(colorAmber)
	steel := fgStyle(colorSteel)

	// Tape edges — solid double-line that visually attaches to the reel
	// quarter-arcs (◜─◝ on top, ◟─◞ on bottom).
	edge := on.Render(strings.Repeat("═", w))

	// Magnetic strip with progress fill.
	filled := int(math.Round(t.progress * float64(w)))
	if filled > w {
		filled = w
	}
	headPos := filled
	if headPos >= w {
		headPos = w - 1
	}
	if headPos < 0 {
		headPos = 0
	}
	var mag strings.Builder
	for i := 0; i < w; i++ {
		switch {
		case i < filled-1:
			mag.WriteString(on.Render("▓"))
		case i == headPos && t.state == tapeTransferring:
			if (t.frame/2)%2 == 0 {
				mag.WriteString(amber.Render("▒"))
			} else {
				mag.WriteString(amber.Render("▓"))
			}
		default:
			mag.WriteString(off.Render("░"))
		}
	}

	// Above-tape shimmer with a recording head triangle when transferring.
	above := strings.Repeat(" ", w)
	if t.state == tapeTransferring && headPos > 0 && headPos < w-1 {
		var b strings.Builder
		for i := 0; i < w; i++ {
			switch {
			case i == headPos:
				b.WriteString(amber.Render("▼"))
			case (i+t.frame/3)%9 == 0:
				b.WriteString(steel.Render("·"))
			default:
				b.WriteByte(' ')
			}
		}
		above = b.String()
	}

	// Below-tape: ground line shadow / cable runners.
	below := strings.Repeat(" ", w)

	return []string{above, edge, mag.String(), edge, below}
}

// ── Mini mode (SAN view) ──────────────────────────────────────────────────────

const (
	tapeMiniWidth  = 12
	tapeMiniHeight = 8
)

func (t tape) ViewMini() string {
	chrome := fgStyle(colorSlate)
	flangeC := fgStyle(colorSteel)
	frost := fgBoldStyle(colorFrost)
	amber := fgBoldStyle(colorAmber)
	mint := fgBoldStyle(colorMint)
	mag := fgBoldStyle(colorMagenta)
	steel := fgStyle(colorSteel)

	var reelCol, fillCol, ledCol lipgloss.Color
	ledOn := false
	statusGlyph := "○"
	switch t.state {
	case tapeIdle:
		reelCol, fillCol, ledCol = colorSteel, colorSlate, colorSlate
		statusGlyph = "○"
	case tapeMounting:
		reelCol, fillCol, ledCol = colorAmber, colorAmber, colorAmber
		ledOn = (t.frame/6)%2 == 0
		statusGlyph = "◐"
	case tapeTransferring:
		reelCol, fillCol, ledCol = colorPhosphor, colorPhosphor, colorAmber
		ledOn = true
		statusGlyph = "●"
	case tapeComplete:
		reelCol, fillCol, ledCol = colorMint, colorMint, colorMint
		ledOn = true
		statusGlyph = "✓"
	case tapeDisconnected:
		reelCol, fillCol, ledCol = colorMagenta, colorMagenta, colorMagenta
		ledOn = t.blink()
		statusGlyph = "✗"
	}

	hubL := fgBoldStyle(reelCol).Render(t.hubGlyph(false))
	hubR := fgBoldStyle(reelCol).Render(t.hubGlyph(true))
	fillStyOn := fgBoldStyle(fillCol)
	fillStyOff := fgStyle(colorSlate)

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

	innerW := tapeMiniWidth - 2 // 10

	// Row 1: top border ┌──────────┐
	r1 := chrome.Render("┌" + strings.Repeat("─", innerW) + "┐")

	// Rows 2-4: two compact reels side-by-side (3-row reels of 4 cells
	// each, with a single-space gutter).  Layout in 10-cell inner:
	//   "  ╭──╮╭──╮  "  total =  2+4+4+2 = 12 — too wide.
	// Use 4-cell reels with no gutter and pad 1 either side:
	//   " ╭──╮╭──╮ " = 1+4+4+1 = 10 ✓
	flange := flangeC
	r2content := " " + flange.Render("╭──╮") + flange.Render("╭──╮") + " "
	r3content := " " + flange.Render("│") + hubL + " " + flange.Render("│") +
		flange.Render("│") + " " + hubR + flange.Render("│") + " "
	r4content := " " + flange.Render("╰──╯") + flange.Render("╰──╯") + " "
	r2 := chrome.Render("│") + r2content + chrome.Render("│")
	r3 := chrome.Render("│") + r3content + chrome.Render("│")
	r4 := chrome.Render("│") + r4content + chrome.Render("│")

	// Row 5: fill bar — innerW cells of progress.
	barW := innerW
	filled := int(math.Round(t.progress * float64(barW)))
	if filled > barW {
		filled = barW
	}
	bar := fillStyOn.Render(strings.Repeat("▓", filled)) +
		fillStyOff.Render(strings.Repeat("░", barW-filled))
	r5 := chrome.Render("│") + bar + chrome.Render("│")

	// Row 6: percentage centred.
	pctTxt := fmt.Sprintf("%d%%", int(math.Round(t.progress*100)))
	r6 := chrome.Render("│") + frost.Render(centre(pctTxt, innerW)) + chrome.Render("│")

	// Row 7: status LED + glyph + label inline.
	ledRune := "·"
	ledStyle := fgStyle(ledCol)
	if ledOn {
		ledRune = "●"
		ledStyle = ledStyle.Bold(true)
	}
	statusSty := steel
	switch t.state {
	case tapeMounting, tapeTransferring:
		statusSty = amber
	case tapeComplete:
		statusSty = mint
	case tapeDisconnected:
		statusSty = mag
	}
	led := ledStyle.Render(ledRune)
	statusTxt := statusSty.Render(statusGlyph)

	// Compose " ● ✓ TAPE-X " — fit label into innerW minus the chrome
	// glyphs: " "(1) + led(1) + " "(1) + status(1) + " "(1) + lbl + " "(1) = 6 + lbl
	lbl := t.label
	maxLbl := innerW - 6
	if maxLbl < 1 {
		maxLbl = 1
	}
	if lipgloss.Width(lbl) > maxLbl {
		lbl = lbl[:maxLbl]
	}
	r7content := " " + led + " " + statusTxt + " " + flangeC.Render(lbl) + " "
	r7 := chrome.Render("│") + pad(r7content, innerW) + chrome.Render("│")

	// Row 8: bottom border.
	r8 := chrome.Render("└" + strings.Repeat("─", innerW) + "┘")

	return strings.Join([]string{r1, r2, r3, r4, r5, r6, r7, r8}, "\n")
}
