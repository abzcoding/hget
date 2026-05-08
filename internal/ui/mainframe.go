package ui

import (
	cryptorand "crypto/rand"
	"encoding/binary"
	"math/rand"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// mainframeWidth is the fixed outer width of the cabinet in cells.
const (
	mainframeWidth   = 50
	mainframeRegLEDs = 16 // programme-register lamps per row
	mainframeRegRows = 4
)

// mainframeState drives LED colour and behaviour.
type mainframeState int

const (
	mfIdle         mainframeState = iota // pre-handshake — slow blinking
	mfHandshaking                        // dialing/handshaking — amber
	mfTransferring                       // active transfer — bright phosphor
	mfComplete                           // done — solid mint
	mfAlarm                              // skip/error/disconnect — magenta
)

// mainframe models a stateful cabinet panel.
type mainframe struct {
	frame int
	state mainframeState
	rng   *rand.Rand
	// Programme-register lamp grid that flickers with activity.
	core [mainframeRegRows][mainframeRegLEDs]float64
	// Internal tape-bay drum spin offsets.  Two bays, each with its own
	// independent rotation so the cabinet looks alive.
	bayPhase [2]int
	// Card-reader feed offset for the slot animation.
	cardFeed int
}

func newMainframe() mainframe {
	var seed int64
	var b [8]byte
	if _, err := cryptorand.Read(b[:]); err == nil {
		seed = int64(binary.LittleEndian.Uint64(b[:]))
	}
	return mainframe{
		state: mfIdle,
		rng:   rand.New(rand.NewSource(seed)),
	}
}

// SetState updates the mainframe's behavioural mode.
func (m *mainframe) SetState(s mainframeState) {
	m.state = s
}

// Tick advances the LED simulation by one frame.
func (m *mainframe) Tick() {
	m.frame++

	// Activity ratio drives how often LEDs light up.
	var ignite, decay float64
	switch m.state {
	case mfIdle:
		ignite, decay = 0.04, 0.06
	case mfHandshaking:
		ignite, decay = 0.12, 0.10
	case mfTransferring:
		ignite, decay = 0.30, 0.10
	case mfComplete:
		ignite, decay = 0.10, 0.04
	case mfAlarm:
		ignite, decay = 0.0, 0.05
	}

	for r := 0; r < mainframeRegRows; r++ {
		for c := 0; c < mainframeRegLEDs; c++ {
			m.core[r][c] -= decay
			if m.core[r][c] < 0 {
				m.core[r][c] = 0
			}
			if m.rng.Float64() < ignite {
				m.core[r][c] = 0.7 + 0.3*m.rng.Float64()
			}
		}
	}

	// Alarm: pulsing red wave across all cells (driven by frame).
	if m.state == mfAlarm {
		on := (m.frame/8)%2 == 0
		val := 0.0
		if on {
			val = 0.95
		}
		for r := 0; r < mainframeRegRows; r++ {
			for c := 0; c < mainframeRegLEDs; c++ {
				m.core[r][c] = val
			}
		}
	}

	// Complete: fade to a steady mint pattern.
	if m.state == mfComplete {
		for r := 0; r < mainframeRegRows; r++ {
			for c := 0; c < mainframeRegLEDs; c++ {
				if m.core[r][c] < 0.6 {
					m.core[r][c] = 0.6
				}
			}
		}
	}

	// Tape bay drum spin — independent phases for visual liveness.
	switch m.state {
	case mfTransferring:
		if m.frame%3 == 0 {
			m.bayPhase[0]++
		}
		if m.frame%4 == 0 {
			m.bayPhase[1]++
		}
		m.cardFeed++
	case mfHandshaking:
		if m.frame%6 == 0 {
			m.bayPhase[0]++
			m.bayPhase[1]++
		}
		if m.frame%2 == 0 {
			m.cardFeed++
		}
	}
}

// View renders the mainframe block as a multi-line string.  Width is fixed
// at mainframeWidth.  The bottom row carries the bus port glyph that
// cable.go aligns its trunk against.
func (m mainframe) View() string {
	chrome := fgStyle(colorPhosphor)
	frame := fgStyle(colorSlate)
	steel := fgStyle(colorSteel)
	frost := fgBoldStyle(colorFrost)
	brand := fgBoldStyle(colorAmber)

	// Pick LED palette + status text per state.
	var ledOn lipgloss.Color
	var statusStr string
	var statusCol lipgloss.Color
	switch m.state {
	case mfIdle:
		ledOn, statusStr, statusCol = colorPhosphor, "STANDBY", colorSteel
	case mfHandshaking:
		ledOn, statusStr, statusCol = colorAmber, "NEGOTIATING", colorAmber
	case mfTransferring:
		ledOn, statusStr, statusCol = colorPhosphor, "XFER ACTIVE", colorMint
	case mfComplete:
		ledOn, statusStr, statusCol = colorMint, "TRANSFER OK", colorMint
	case mfAlarm:
		ledOn, statusStr, statusCol = colorMagenta, "LINK FAULT", colorMagenta
	}
	ledDim := colorSlate

	ledRune := func(b float64) string {
		switch {
		case b >= 0.7:
			return fgBoldStyle(ledOn).Render("●")
		case b >= 0.3:
			return fgStyle(ledOn).Render("●")
		default:
			return fgStyle(ledDim).Render("·")
		}
	}

	inner := mainframeWidth - 2

	pad := func(content string, want int) string {
		gap := want - lipgloss.Width(content)
		if gap < 0 {
			gap = 0
		}
		return content + strings.Repeat(" ", gap)
	}
	centre := func(content string, want int) string {
		gap := want - lipgloss.Width(content)
		if gap < 0 {
			gap = 0
		}
		l := gap / 2
		r := gap - l
		return strings.Repeat(" ", l) + content + strings.Repeat(" ", r)
	}

	var b strings.Builder

	// ── Row 0: top frame with corner rivets. ────────────────────────────
	top := chrome.Render("╔") +
		chrome.Render(strings.Repeat("═", inner)) +
		chrome.Render("╗")
	b.WriteString(top + "\n")

	// ── Row 1: brand plate strap. ───────────────────────────────────────
	plate := brand.Render("▓▓ HGET·SYSTEM/9000 ▓▓") +
		steel.Render("  MOD·IV  ") +
		frost.Render("S/N 4096")
	b.WriteString(chrome.Render("║") + centre(plate, inner) + chrome.Render("║") + "\n")

	// ── Row 2: divider. ─────────────────────────────────────────────────
	b.WriteString(chrome.Render("╠") +
		frame.Render(strings.Repeat("═", inner)) +
		chrome.Render("╣") + "\n")

	// ── Rows 3–9: master console panel. ─────────────────────────────────
	// Inner content area inside the cabinet: inner-2 cells (1-cell
	// padding either side).  Sub-panel lives inside that.
	subInnerW := inner - 4 // sub-panel inner content (after "║ ┌" + " ┐ ║")

	// Row 3: top of console with [ MASTER CONSOLE ] tag
	consoleLabel := steel.Render("[ MASTER CONSOLE ]")
	tagW := lipgloss.Width(consoleLabel)
	leftDash := 2
	rightDash := subInnerW - leftDash - tagW
	if rightDash < 1 {
		rightDash = 1
	}
	b.WriteString(chrome.Render("║ ") +
		frame.Render("┌") +
		frame.Render(strings.Repeat("─", leftDash)) +
		consoleLabel +
		frame.Render(strings.Repeat("─", rightDash)) +
		frame.Render("┐") +
		chrome.Render(" ║") + "\n")

	// Row 4: status LED chiclets row.
	statusChip := func(name string, on bool, accent lipgloss.Color) string {
		col := ledDim
		if on {
			col = accent
		}
		dot := fgBoldStyle(col).Render("◉")
		return dot + " " + steel.Render(name)
	}
	pwrOn := m.state != mfAlarm
	runOn := m.state == mfTransferring
	ioOn := m.state == mfTransferring || m.state == mfHandshaking
	if m.state == mfHandshaking && (m.frame/3)%2 == 0 {
		ioOn = false // io blinks during handshake
	}
	alarmOn := m.state == mfAlarm && (m.frame/8)%2 == 0
	chips := strings.Join([]string{
		statusChip("POWER", pwrOn, colorMint),
		statusChip("RUN", runOn, colorMint),
		statusChip("I/O", ioOn, colorAmber),
		statusChip("ALARM", alarmOn, colorMagenta),
	}, "  ")
	b.WriteString(chrome.Render("║ ") +
		frame.Render("│") +
		" " + pad(chips, subInnerW-2) + " " +
		frame.Render("│") +
		chrome.Render(" ║") + "\n")

	// Rows 5–8: programme register grid (4 rows × 16 LEDs each).
	regLabels := []string{"ADDR", "DATA", "INSTR", "STAT"}
	for r := 0; r < mainframeRegRows; r++ {
		// Render LEDs as "● " repeated; 16*2 - 1 = 31 cells of LEDs.
		var row strings.Builder
		for c := 0; c < mainframeRegLEDs; c++ {
			row.WriteString(ledRune(m.core[r][c]))
			if c < mainframeRegLEDs-1 {
				row.WriteString(" ")
			}
		}
		ledRow := row.String()
		labelStyled := steel.Render(regLabels[r])

		// Inner panel inside console: " ┌──...──┐ " — sub-panel's interior
		// is subInnerW-2 cells.  Inside that, layout: "  ledRow  label  "
		content := "  " + ledRow + "  " + labelStyled
		b.WriteString(chrome.Render("║ ") +
			frame.Render("│") +
			pad(content, subInnerW) +
			frame.Render("│") +
			chrome.Render(" ║") + "\n")
	}

	// Row 9: bottom of console.
	b.WriteString(chrome.Render("║ ") +
		frame.Render("└"+strings.Repeat("─", subInnerW)+"┘") +
		chrome.Render(" ║") + "\n")

	// ── Rows 10–13: tape bays + card reader. ────────────────────────────
	// Three sub-bays side-by-side.  Layout per bay: 13 cells wide.
	// Inside cabinet inner = 48 cells (inner=48 if mainframeWidth=50).
	// 3 bays × 13 = 39, plus 2 separators = 41, fits in 48 with 7 cells of
	// padding distributed.
	bayW := 13
	totalBayW := 3*bayW + 2 // two single-space gaps
	bayPad := (inner - totalBayW) / 2
	if bayPad < 1 {
		bayPad = 1
	}
	bayPadStr := strings.Repeat(" ", bayPad)

	// Reuse the same drum animation for bay reels.
	drumGlyph := func(idx int) string {
		// Use ◐◓◑◒ sequence; freeze on alarm/idle.
		switch m.state {
		case mfAlarm, mfIdle:
			return "◯"
		case mfComplete:
			return "◉"
		}
		seq := []string{"◐", "◓", "◑", "◒"}
		return seq[m.bayPhase[idx]%len(seq)]
	}

	// Card slot fill animation for the reader bay.
	cardSlotW := bayW - 4 // inner of "│ " + content + " │"
	cardFill := strings.Repeat("▓", cardSlotW)
	if m.state == mfTransferring || m.state == mfHandshaking {
		// rolling shimmer pattern
		off := m.cardFeed % cardSlotW
		runes := []rune(strings.Repeat("░", cardSlotW))
		for i := 0; i < cardSlotW; i++ {
			if (i+off)%4 == 0 {
				runes[i] = '▓'
			} else if (i+off)%4 == 1 {
				runes[i] = '▒'
			}
		}
		cardFill = string(runes)
	} else if m.state == mfAlarm {
		cardFill = strings.Repeat("░", cardSlotW)
	}

	// Bay frame helper: 4-row sub-bay with label tag + reel/feed window.
	renderBay := func(label, content string) []string {
		// 4 rows: top, content top, content bot, bottom.
		// bayW = 13; inner width inside bay = 11.
		bayInner := bayW - 2
		// Top with tag
		tag := frame.Render("[" + label + "]")
		tagW := lipgloss.Width(tag)
		ld := 1
		rd := bayInner - ld - tagW
		if rd < 0 {
			rd = 0
		}
		top := frame.Render("┌") +
			frame.Render(strings.Repeat("─", ld)) +
			tag +
			frame.Render(strings.Repeat("─", rd)) +
			frame.Render("┐")
		bot := frame.Render("└" + strings.Repeat("─", bayInner) + "┘")
		// Two content rows.  `content` is already pre-styled.
		// We'll wrap with ║ side rails of the bay.
		rows := strings.Split(content, "\n")
		for len(rows) < 2 {
			rows = append(rows, "")
		}
		styled := make([]string, 0, 4)
		styled = append(styled, top)
		for _, r := range rows[:2] {
			styled = append(styled, frame.Render("│")+
				centre(r, bayInner)+
				frame.Render("│"))
		}
		styled = append(styled, bot)
		return styled
	}

	// Tape bay content: a small reel window centered.
	reelArt := func(idx int) string {
		drumStyled := fgBoldStyle(ledOn).Render(drumGlyph(idx))
		// 2-row reel: ╭───╮ / │ ◐ │ — but we need 2 rows fitting in a bay
		// content area of 2 rows.  So:
		//   row1: ╭───╮
		//   row2: │ X │
		row1 := frame.Render("╭───╮")
		row2 := frame.Render("│") + " " + drumStyled + " " + frame.Render("│")
		return row1 + "\n" + row2
	}

	cardArt := func() string {
		// 2-row card slot.  No side rails — renderBay wraps already.
		// Row 1: a notched slot edge (the card-feed lip).
		// Row 2: animated punch-card fill that scrolls when transferring.
		fillSty := fgStyle(ledOn).Render(cardFill)
		row1 := frame.Render("╶" + strings.Repeat("─", cardSlotW) + "╴")
		row2 := " " + fillSty + " "
		return row1 + "\n" + row2
	}

	bayA := renderBay("TAPE-A", reelArt(0))
	bayB := renderBay("TAPE-B", reelArt(1))
	bayC := renderBay("CARD", cardArt())

	for r := 0; r < 4; r++ {
		row := bayPadStr + bayA[r] + " " + bayB[r] + " " + bayC[r]
		b.WriteString(chrome.Render("║") + pad(row, inner) + chrome.Render("║") + "\n")
	}

	// ── Row 14: rivet strip. ────────────────────────────────────────────
	rivetCount := (inner - 2) / 2
	rivets := strings.Repeat(steel.Render("▪")+" ", rivetCount)
	b.WriteString(chrome.Render("║ ") + pad(rivets, inner-2) + chrome.Render(" ║") + "\n")

	// ── Row 15: status caption row. ─────────────────────────────────────
	statusContent := steel.Render("STATUS: ") +
		fgBoldStyle(statusCol).Render(statusStr)
	b.WriteString(chrome.Render("║ ") + pad(statusContent, inner-2) + chrome.Render(" ║") + "\n")

	// ── Row 16: divider before bus. ─────────────────────────────────────
	half := (inner - 1) / 2
	b.WriteString(chrome.Render("╠") +
		chrome.Render(strings.Repeat("═", half)) +
		chrome.Render("╤") +
		chrome.Render(strings.Repeat("═", inner-half-1)) +
		chrome.Render("╣") + "\n")

	// ── Row 17: bottom plate with bus port socket. ──────────────────────
	port := chrome.Render("┴")
	bottom := chrome.Render("╚") +
		chrome.Render(strings.Repeat("═", half)) +
		port +
		chrome.Render(strings.Repeat("═", inner-half-1)) +
		chrome.Render("╝")
	b.WriteString(bottom)

	return b.String()
}

// PortColumn returns the 0-indexed column (within the rendered block) of
// the bus port glyph.  Used by cable.go to align the trunk under the
// mainframe.
func (m mainframe) PortColumn() int {
	inner := mainframeWidth - 2
	return 1 + (inner-1)/2
}
