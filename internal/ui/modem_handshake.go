package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const modemDeviceWidth = 50 // outer modem chassis width

// modemHandshake renders an animated dial-up modem connection sequence.
type modemHandshake struct {
	frame int
	phase int // 0=dialing 1=carrier 2=handshake 3=connected
	mf    mainframe
	width int
}

func newModemHandshake() modemHandshake {
	mf := newMainframe()
	mf.SetState(mfIdle)
	return modemHandshake{mf: mf}
}

func (m *modemHandshake) SetWidth(w int) { m.width = w }
func (m modemHandshake) Phase() int      { return m.phase }

func (m *modemHandshake) Tick() {
	m.frame++
	if m.frame%15 == 0 && m.phase < 3 {
		m.phase++
	}
	switch m.phase {
	case 0, 1:
		m.mf.SetState(mfIdle)
	case 2, 3:
		m.mf.SetState(mfHandshaking)
	}
	m.mf.Tick()
}

func (m modemHandshake) View(url string) string {
	steel := lipgloss.NewStyle().Foreground(colorSteel)
	frost := lipgloss.NewStyle().Foreground(colorFrost).Bold(true)
	amber := lipgloss.NewStyle().Foreground(colorAmber).Bold(true)
	mint := lipgloss.NewStyle().Foreground(colorMint).Bold(true)
	chrome := lipgloss.NewStyle().Foreground(colorPhosphor)
	frame := lipgloss.NewStyle().Foreground(colorSlate)

	// ── Build mainframe centered to total width. ─────────────────────────
	w := m.width
	if w < mainframeWidth+6 {
		w = mainframeWidth + 6
	}
	mfPad := (w - mainframeWidth) / 2
	if mfPad < 0 {
		mfPad = 0
	}
	mfPadStr := strings.Repeat(" ", mfPad)
	mfLines := strings.Split(m.mf.View(), "\n")

	// ── Vertical phone-line column connecting mainframe to modem. ───────
	// The column drops from the mainframe's port directly to the centre of
	// the modem chassis.  3 rows tall.
	const lineRows = 3
	mfPort := mfPad + m.mf.PortColumn()

	modemPad := (w - modemDeviceWidth) / 2
	if modemPad < 0 {
		modemPad = 0
	}
	// Anchor the line to the modem's actual phone-jack column (not the
	// raw chassis centre) so the cable plugs into the same notch the
	// chassis bottom renders.
	modemPort := modemPad + m.PortColumn()

	lineLines := m.renderPhoneLine(lineRows, w, mfPort, modemPort)

	// ── Build modem block. ──────────────────────────────────────────────
	modemBlock := m.renderModem()
	modemLines := strings.Split(modemBlock, "\n")
	modemPadStr := strings.Repeat(" ", modemPad)

	var b strings.Builder
	for _, line := range mfLines {
		b.WriteString(mfPadStr + line + "\n")
	}
	for _, line := range lineLines {
		b.WriteString(line + "\n")
	}
	for _, line := range modemLines {
		b.WriteString(modemPadStr + line + "\n")
	}

	// ── Status caption + phase ladder. ──────────────────────────────────
	var bannerColor lipgloss.Color
	var status string
	switch m.phase {
	case 0:
		bannerColor, status = colorAmber, "DIALING"
	case 1:
		bannerColor, status = colorAmber, "CARRIER DETECT"
	case 2:
		bannerColor, status = colorPhosphor, "HANDSHAKE"
	case 3:
		bannerColor, status = colorMint, "LINK ESTABLISHED"
	}
	bannerSty := lipgloss.NewStyle().Foreground(bannerColor).Bold(true)
	caption := bannerSty.Render("◆ "+status) +
		steel.Render("  · target ") +
		frost.Render(truncateURL(url, 64))
	b.WriteString("\n  " + caption + "\n")

	dots := []string{"·", "·", "·", "·"}
	for i := 0; i <= m.phase; i++ {
		switch {
		case i == m.phase && m.phase < 3:
			dots[i] = amber.Render("◐")
		case m.phase == 3:
			dots[i] = mint.Render("●")
		default:
			dots[i] = chrome.Render("●")
		}
	}
	for i := m.phase + 1; i < 4; i++ {
		dots[i] = frame.Render("·")
	}
	ladder := steel.Render("phase  ") +
		strings.Join(dots, steel.Render(" ─ ")) +
		"   " + steel.Render(fmt.Sprintf("%d/4", m.phase+1))
	b.WriteString("  " + ladder + "\n")

	return b.String()
}

func (m modemHandshake) renderModem() string {
	chrome := lipgloss.NewStyle().Foreground(colorPhosphor)
	frame := lipgloss.NewStyle().Foreground(colorSlate)
	steel := lipgloss.NewStyle().Foreground(colorSteel)
	frost := lipgloss.NewStyle().Foreground(colorFrost).Bold(true)
	brand := lipgloss.NewStyle().Foreground(colorAmber).Bold(true)
	amber := lipgloss.NewStyle().Foreground(colorAmber).Bold(true)

	// LED palette per phase.
	ledOn := func(on bool, accent lipgloss.Color) string {
		col := colorSlate
		if on {
			col = accent
		}
		return lipgloss.NewStyle().Foreground(col).Bold(true).Render("◉")
	}

	// Phase-driven LED pattern.  Hayes panel LEDs:
	//   HS High Speed
	//   AA Auto Answer
	//   CD Carrier Detect
	//   OH Off Hook
	//   RD Receive Data
	//   TR Terminal Ready
	//   MR Modem Ready
	hs, aa, cd, oh, rd, tr, mr := false, false, false, false, false, false, false
	mr = true        // power on always
	tr = m.phase > 0 // PC connected
	switch m.phase {
	case 0: // dialing
		oh = true
		// HS blinks while we wait
		hs = (m.frame/4)%2 == 0
	case 1: // carrier detect
		oh, cd = true, (m.frame/4)%2 == 0
		hs = true
	case 2: // handshake
		oh, cd = true, true
		rd = (m.frame/3)%2 == 0
		hs = true
	case 3: // connected
		oh, cd, rd, hs = true, true, true, true
		aa = true
	}

	// LED accent colour per phase.
	ledAccent := colorAmber
	if m.phase >= 2 {
		ledAccent = colorPhosphor
	}
	if m.phase == 3 {
		ledAccent = colorMint
	}

	innerW := modemDeviceWidth - 2 // 34

	pad := func(s string, w int) string {
		gap := w - lipgloss.Width(s)
		if gap < 0 {
			gap = 0
		}
		return s + strings.Repeat(" ", gap)
	}

	// ── Row 0: top of chassis. ──────────────────────────────────────────
	r0 := chrome.Render("┌" + strings.Repeat("─", innerW) + "┐")

	// ── Row 1: brand strap with corner power-rivet. ────────────────────
	plate := frame.Render("╓─ ") + brand.Render("HAYES SMARTMODEM 56K") + frame.Render(" ─╖")
	rivet := lipgloss.NewStyle().Foreground(ledAccent).Bold(true).Render("▣")
	leftPad := 3
	rightPad := 3
	plateW := lipgloss.Width(plate)
	rivetW := lipgloss.Width(rivet)
	gap := innerW - leftPad - plateW - rightPad - rivetW
	if gap < 1 {
		gap = 1
	}
	r1content := strings.Repeat(" ", leftPad) + plate +
		strings.Repeat(" ", gap) + rivet + strings.Repeat(" ", rightPad)
	r1 := chrome.Render("│") + pad(r1content, innerW) + chrome.Render("│")

	// ── Row 2: divider. ─────────────────────────────────────────────────
	r2 := chrome.Render("╞" + strings.Repeat("═", innerW) + "╡")

	// ── Row 3 + 4: LED row with labels above + grille on the right. ─────
	// 7 LEDs: HS AA CD OH RD TR MR.  Labels (row 3) over LEDs (row 4),
	// fixed pitch of 4 cells per column ("HS  ").  Speaker grille drawn
	// to the right.
	labels := []string{"HS", "AA", "CD", "OH", "RD", "TR", "MR"}
	leds := []bool{hs, aa, cd, oh, rd, tr, mr}

	// Build per-column label/LED stack, each 4 cells wide.
	var lblRow, dotRow strings.Builder
	for i, l := range labels {
		lblRow.WriteString(steel.Render(l))
		// Centre LED beneath the 2-char label.
		dotRow.WriteString(ledOn(leds[i], ledAccent) + " ")
		if i < len(labels)-1 {
			lblRow.WriteString("  ") // 2-space gap
			// dotRow gap matches: " " (already trailing) so add 1 more
			// to make 2 total.
			// Effective column = label(2) + gap(2) = 4 cells wide.
			// LED row column = LED(1) + space(1) + gap(1)? Need 4 cells.
		}
	}
	// Rebuild dotRow with consistent pitch: each cell = "● " then 2-space gap.
	dotRow.Reset()
	for i, on := range leds {
		dotRow.WriteString(ledOn(on, ledAccent))
		if i < len(leds)-1 {
			dotRow.WriteString(strings.Repeat(" ", 3))
		}
	}
	// Speaker grille — small 5×2 cone.
	grille1 := frame.Render("┌───┐")
	grille2 := frame.Render("│░░░│")
	if rd {
		grille2 = frame.Render("│") + amber.Render("░▒░") + frame.Render("│")
	}

	// Layout: each LED column is 4 cells wide.  Labels start at column 2
	// (after 2-cell left pad), LEDs start at column 2 too so the LED's
	// single-cell glyph sits centred under the 2-char label's first char.
	// 7 columns × 4 cells = 28 cells of LEDs, + 2 left pad = 30.
	const labelPad = 2
	leftBlock1 := strings.Repeat(" ", labelPad) + lblRow.String()
	dotRowStr := dotRow.String()
	// Pad dotRow to the same display width as lblRow so the speaker
	// grille on the right lines up vertically across both rows.
	if dw := lipgloss.Width(lblRow.String()) - lipgloss.Width(dotRowStr); dw > 0 {
		dotRowStr += strings.Repeat(" ", dw)
	}
	leftBlock2 := strings.Repeat(" ", labelPad) + dotRowStr
	// Right-justify the speaker grille — same gap on both rows so the
	// 5-cell grille top/bottom line up vertically.
	const grilleW = 5
	grilleGap := innerW - lipgloss.Width(leftBlock1) - grilleW - 2
	if grilleGap < 1 {
		grilleGap = 1
	}
	r3content := leftBlock1 + strings.Repeat(" ", grilleGap) + grille1 + " "
	r4content := leftBlock2 + strings.Repeat(" ", grilleGap) + grille2 + " "
	r3 := chrome.Render("│") + pad(r3content, innerW) + chrome.Render("│")
	r4 := chrome.Render("│") + pad(r4content, innerW) + chrome.Render("│")

	// ── Row 5: divider. ─────────────────────────────────────────────────
	r5 := chrome.Render("╞" + strings.Repeat("═", innerW) + "╡")

	// ── Rows 6–8: jacks + dial spinner. ─────────────────────────────────
	jackBox := func(label string) []string {
		const outerW = 13
		inner := outerW - 2 // 11
		tag := steel.Render(fmt.Sprintf("[%s]", label))
		tagW := lipgloss.Width(tag)
		ld := 1
		rd := inner - ld - tagW
		if rd < 0 {
			rd = 0
		}
		top := frame.Render("┌") +
			frame.Render(strings.Repeat("─", ld)) +
			tag +
			frame.Render(strings.Repeat("─", rd)) +
			frame.Render("┐")
		// 4 pins centred — "  ▢ ▢ ▢ ▢  " (11 chars)
		pins := frost.Render("▢") + " " + frost.Render("▢") + " " + frost.Render("▢") + " " + frost.Render("▢")
		mid := frame.Render("│") + "  " + pins + "  " + frame.Render("│")
		bot := frame.Render("└" + strings.Repeat("─", inner) + "┘")
		return []string{top, mid, bot}
	}

	jackL := jackBox("LINE")
	jackP := jackBox("PHONE")

	// Dial-spinner pod — 9 cells wide × 3 tall.  An animated dial wheel.
	dialChars := []string{"◜", "◝", "◞", "◟"}
	var dialA, dialB string
	switch {
	case m.phase == 0:
		idx := m.frame % 4
		dialA = amber.Render(dialChars[idx])
		dialB = amber.Render(dialChars[(idx+2)%4])
	case m.phase >= 2:
		dialA = lipgloss.NewStyle().Foreground(colorMint).Bold(true).Render("◉")
		dialB = lipgloss.NewStyle().Foreground(colorMint).Bold(true).Render("◉")
	default:
		dialA = steel.Render("·")
		dialB = steel.Render("·")
	}
	dialPod := []string{
		"  " + dialA + " " + dialB + "  ",
		" " + frost.Render("◉ DIAL") + "  ",
		"  " + steel.Render("──────") + " ",
	}
	// Each pod row should be 9 cells exactly.
	for i, r := range dialPod {
		if w := lipgloss.Width(r); w < 9 {
			dialPod[i] = r + strings.Repeat(" ", 9-w)
		}
	}

	composeJackRow := func(idx int) string {
		// "  " + jackL(13) + "  " + jackP(13) + "  " + dial(9) + ?  = 41 cells
		// innerW=48; spare = 7 cells trailing.
		content := "  " + jackL[idx] + "  " + jackP[idx] + "  " + dialPod[idx]
		return chrome.Render("│") + pad(content, innerW) + chrome.Render("│")
	}
	row6 := composeJackRow(0)
	row7 := composeJackRow(1)
	row8 := composeJackRow(2)

	// ── Row 9: bottom with phone-jack notch (where the line plugs in). ──
	half := (innerW - 1) / 2
	r9 := chrome.Render("└" +
		strings.Repeat("─", half) +
		"┬" +
		strings.Repeat("─", innerW-half-1) +
		"┘")

	return strings.Join([]string{r0, r1, r2, r3, r4, r5, row6, row7, row8, r9}, "\n")
}

// PortColumn returns the modem's phone-jack column (0-indexed within the
// modem block).  Used to anchor the phone line.
func (m modemHandshake) PortColumn() int {
	innerW := modemDeviceWidth - 2
	return 1 + (innerW-1)/2
}

func (m modemHandshake) renderPhoneLine(rows, width, mfCol, modemCol int) []string {
	if rows < 2 {
		rows = 2
	}
	if mfCol < 0 {
		mfCol = 0
	}
	if mfCol >= width {
		mfCol = width - 1
	}
	if modemCol < 0 {
		modemCol = 0
	}
	if modemCol >= width {
		modemCol = width - 1
	}

	steel := lipgloss.NewStyle().Foreground(colorSteel)
	chromeOn := lipgloss.NewStyle().Foreground(colorPhosphor)
	chromeDim := lipgloss.NewStyle().Foreground(colorSlate)
	amber := lipgloss.NewStyle().Foreground(colorAmber).Bold(true)
	mint := lipgloss.NewStyle().Foreground(colorMint).Bold(true)

	// Pick palette per phase.
	var trunkSty lipgloss.Style
	switch m.phase {
	case 0:
		trunkSty = chromeDim
	case 1:
		trunkSty = lipgloss.NewStyle().Foreground(colorAmber)
	case 2:
		trunkSty = chromeOn
	case 3:
		trunkSty = mint
	}

	// Build the geometry: drop down rows-1 cells in the mainframe's column,
	// then bend horizontally at the second-to-last row, then drop into the
	// modem's column at the last row.
	out := make([][]rune, rows)
	stylized := make([][]string, rows)
	for r := 0; r < rows; r++ {
		out[r] = make([]rune, width)
		stylized[r] = make([]string, width)
		for c := 0; c < width; c++ {
			out[r][c] = ' '
			stylized[r][c] = " "
		}
	}

	// Row 0: vertical from mainframe.
	out[0][mfCol] = '║'

	// Last row: vertical into modem.
	out[rows-1][modemCol] = '║'

	// Middle rows: horizontal bridge if columns differ.
	if mfCol != modemCol {
		mid := rows / 2
		minC, maxC := mfCol, modemCol
		if minC > maxC {
			minC, maxC = maxC, minC
		}
		// Vertical drop in mainframe column down to mid row.
		for r := 0; r < mid; r++ {
			out[r][mfCol] = '║'
		}
		// Vertical drop in modem column from mid row to bottom.
		for r := mid; r < rows; r++ {
			out[r][modemCol] = '║'
		}
		// Horizontal bridge at mid row.
		for c := minC; c <= maxC; c++ {
			out[mid][c] = '═'
		}
		// Corner glyphs.  Direction depends on which is greater.
		if mfCol < modemCol {
			out[mid][mfCol] = '╚'
			out[mid][modemCol] = '╗'
		} else {
			out[mid][mfCol] = '╝'
			out[mid][modemCol] = '╔'
		}
	} else {
		for r := 0; r < rows; r++ {
			out[r][mfCol] = '║'
		}
	}

	// Phase 0/1/2/3 ornament: traveling packet glyph drifting along the
	// line in the mainframe column.
	pktRow := -1
	pktCol := -1
	switch m.phase {
	case 0:
		// sparse blips
		pktRow = (m.frame / 6) % rows
		pktCol = mfCol
	case 1:
		pktRow = (m.frame / 4) % rows
		pktCol = mfCol
	case 2:
		// arrows bouncing — show them at varying rows
		pktRow = (m.frame / 3) % rows
		pktCol = mfCol
	case 3:
		pktRow = (m.frame / 2) % rows
		pktCol = mfCol
	}

	// Render row by row.
	rendered := make([]string, rows)
	for r := 0; r < rows; r++ {
		var b strings.Builder
		for c := 0; c < width; c++ {
			ch := out[r][c]
			if ch == ' ' {
				// Optional fringe dots above/below trunk for depth.
				if (c == mfCol-2 || c == mfCol+2) && r%2 == 0 {
					b.WriteString(steel.Render("·"))
				} else {
					b.WriteByte(' ')
				}
				continue
			}
			styled := trunkSty.Render(string(ch))
			if r == pktRow && c == pktCol {
				switch m.phase {
				case 0:
					styled = steel.Render("·")
				case 1:
					styled = amber.Render("●")
				case 2:
					if (m.frame/2)%2 == 0 {
						styled = chromeOn.Render("►")
					} else {
						styled = chromeOn.Render("◄")
					}
				case 3:
					styled = mint.Render("●")
				}
			}
			b.WriteString(styled)
		}
		rendered[r] = b.String()
	}
	return rendered
}

// truncateURL trims a URL to fit inside w cells.
func truncateURL(s string, w int) string {
	if w <= 0 || lipgloss.Width(s) <= w {
		return s
	}
	if w < 8 {
		return s[:w]
	}
	keep := w - 1
	return s[:keep] + "…"
}
