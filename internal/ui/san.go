package ui

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// sanItemStatus mirrors batch item lifecycle.
type sanItemStatus int

const (
	sanQueued sanItemStatus = iota
	sanActive
	sanDone
	sanFailed
	sanSkipped
)

// sanItem is one drive bay in the cabinet.
type sanItem struct {
	Label  string
	Status sanItemStatus
}

// san composes the storage-array cabinet for batch view.
type san struct {
	frame    int
	mf       mainframe // kept for backward compatibility, not rendered
	bus      cable     // ditto
	items    []sanItem
	active   int     // 0-based index of the active item; -1 if none
	progress float64 // active bay's progress (spring-smoothed pct)
	speed    float64
	peak     float64
	width    int
	height   int  // height budget for the SAN block (0 = unconstrained)
	compact  bool // when true, omit the chassis frame around the bays
}

// Bay geometry.  Width and height are fixed; layout chooses how many bays
// fit per row of the cabinet based on terminal width.
const (
	sanBayW = 22
	sanBayH = 7
)

func newSan(items []sanItem) san {
	s := san{
		mf:     newMainframe(),
		bus:    newCable(),
		items:  items,
		active: -1,
	}
	s.mf.SetState(mfTransferring)
	s.bus.SetState(cableActive)
	return s
}

func (s *san) SetActive(i int) { s.active = i }

func (s *san) Update(progress, speed, peak float64, mfState mainframeState, busState cableState) {
	s.progress = progress
	s.speed = speed
	s.peak = peak
	s.mf.SetState(mfState)
	s.bus.SetState(busState)
}

func (s *san) SetWidth(w int) { s.width = w }

// SetHeight tells the SAN how many rows it has to fit into.  0 disables
// the constraint (renders all bays).
func (s *san) SetHeight(h int) { s.height = h }

// SetCompact toggles the compact (no-chassis) layout.
func (s *san) SetCompact(c bool) { s.compact = c }

// chassisChromeRows returns the number of non-bay rows the chassis frame
// consumes (top + brand + sub-rule + sub-rule + status + bottom = 6).
const chassisChromeRows = 6

// vetGap is the vent strip between bay rows.
const vetGap = 1

// fittingBayRows returns how many bay rows fit in the given budget.
// chassisOn=true accounts for the cabinet chrome rows.
func fittingBayRows(budget int, chassisOn bool) int {
	if budget <= 0 {
		return 0
	}
	avail := budget
	if chassisOn {
		avail -= chassisChromeRows
	}
	if avail < sanBayH {
		return 0
	}
	// avail = N*sanBayH + (N-1)*vetGap  ⇒  N = (avail+vetGap) / (sanBayH+vetGap)
	n := (avail + vetGap) / (sanBayH + vetGap)
	if n < 1 {
		n = 1
	}
	if n > 3 {
		n = 3
	}
	return n
}

// FitsCompact reports whether at least one bay row fits in `h` rows
// without the chassis frame.  Used by the tier picker to decide whether
// the SAN can display anything useful.
func (s san) FitsCompact(h int) bool { return fittingBayRows(h, false) >= 1 }

// FitsChassis reports whether at least one bay row fits in `h` rows
// with the full cabinet frame.
func (s san) FitsChassis(h int) bool { return fittingBayRows(h, true) >= 1 }

func (s *san) Tick(rateRatio float64) {
	s.frame++
	s.mf.Tick()
	s.bus.Tick(rateRatio)
}

// View renders the storage-array cabinet.
func (s san) View() string {
	w := s.width
	if w < 30 {
		w = 30
	}

	// ── Columns: dynamic based on available width. ──────────────────────
	// Cap at 3 so cabinet alignment with the 72-cell data-link panel below
	// is preserved.  At narrow widths the cabinet shrinks: 2 cols → 48
	// inner, 1 col → 24 inner.
	const maxCols = 3
	usable := w - 2 // outer ║ rails
	if s.compact {
		usable = w
	}
	cols := (usable + 2) / (sanBayW + 2)
	if cols < 1 {
		cols = 1
	}
	if cols > maxCols {
		cols = maxCols
	}

	// ── Rows: dynamic based on available height. ────────────────────────
	maxRows := fittingBayRows(s.height, !s.compact)
	if s.height <= 0 {
		// Unconstrained: default to 3 rows so big cabinets render fully
		// in tests / non-TTY snapshots.
		maxRows = 3
	}
	if maxRows < 1 {
		maxRows = 1
	}
	maxBays := cols * maxRows

	// Window the items around the active one.
	startIdx, endIdx := s.windowItems(maxBays)
	visible := s.items[startIdx:endIdx]

	// Render each visible bay.
	bays := make([][]string, len(visible))
	for i, it := range visible {
		// Whether this is THE actively-transferring bay.
		isActive := (startIdx+i) == s.active && it.Status == sanActive
		bays[i] = s.renderBay(it, isActive)
	}

	// Lay out bays in row-major order with cols-per-row.
	gridRows := (len(visible) + cols - 1) / cols
	rowBlocks := make([]string, gridRows)
	for r := 0; r < gridRows; r++ {
		startB := r * cols
		endB := startB + cols
		if endB > len(visible) {
			endB = len(visible)
		}
		// Stitch bays in this row line-by-line.
		var rowLines [sanBayH]string
		for line := 0; line < sanBayH; line++ {
			var b strings.Builder
			for i := startB; i < endB; i++ {
				if i > startB {
					b.WriteString("  ") // 2-cell gap between bays
				}
				b.WriteString(bays[i][line])
			}
			rowLines[line] = b.String()
		}
		rowBlocks[r] = strings.Join(rowLines[:], "\n")
	}

	// Total grid width.
	gridContentW := cols*sanBayW + (cols-1)*2
	if cols > len(visible) {
		gridContentW = len(visible)*sanBayW + (len(visible)-1)*2
	}

	// Window indicator.
	var indicator string
	if startIdx > 0 || endIdx < len(s.items) {
		indicator = sanWindowIndicator(startIdx, endIdx, len(s.items), w)
	}

	if s.compact {
		// No chassis — just centre the grid in the terminal width.
		return s.composeCompact(rowBlocks, gridContentW, indicator)
	}
	return s.composeCabinet(rowBlocks, gridContentW, indicator)
}

// composeCabinet wraps the bay grid in a full storage-array chassis with
// brand strap, vent rows, and status footer.
func (s san) composeCabinet(rowBlocks []string, gridW int, indicator string) string {
	chrome := fgStyle(colorPhosphor)
	frame := fgStyle(colorSlate)
	steel := fgStyle(colorSteel)
	frost := fgBoldStyle(colorFrost)
	brand := fgBoldStyle(colorAmber)
	mint := fgBoldStyle(colorMint)
	mag := fgBoldStyle(colorMagenta)
	amber := fgBoldStyle(colorAmber)

	// Inner cabinet width.  No extra padding around the grid: the bays
	// fill the chassis flush so total width matches tapeBannerWidth (72)
	// for visual harmony with the data-link panel below.
	inner := gridW
	cabW := inner + 2 // outer ║ borders

	// Centre cabinet in terminal.
	scenePad := (s.width - cabW) / 2
	if scenePad < 0 {
		scenePad = 0
	}
	padStr := strings.Repeat(" ", scenePad)

	pad := func(c string, w int) string {
		gap := w - lipgloss.Width(c)
		if gap < 0 {
			gap = 0
		}
		return c + strings.Repeat(" ", gap)
	}
	centre := func(c string, w int) string {
		gap := w - lipgloss.Width(c)
		if gap < 0 {
			gap = 0
		}
		l := gap / 2
		r := gap - l
		return strings.Repeat(" ", l) + c + strings.Repeat(" ", r)
	}

	var out strings.Builder

	// ── Top frame. ──────────────────────────────────────────────────────
	out.WriteString(padStr +
		chrome.Render("╔") +
		chrome.Render(strings.Repeat("═", inner)) +
		chrome.Render("╗") + "\n")

	// ── Brand strap. ────────────────────────────────────────────────────
	bayCount := fmt.Sprintf("%d BAYS", len(s.items))
	plate := brand.Render("▓▓ HGET·SAN-9000 ▓▓") +
		steel.Render("  STORAGE ARRAY  ") +
		frost.Render(bayCount)
	out.WriteString(padStr +
		chrome.Render("║") +
		centre(plate, inner) +
		chrome.Render("║") + "\n")

	// ── Sub-rule. ───────────────────────────────────────────────────────
	out.WriteString(padStr +
		chrome.Render("╠") +
		frame.Render(strings.Repeat("─", inner)) +
		chrome.Render("╣") + "\n")

	// ── Bay grid. ───────────────────────────────────────────────────────
	for ri, block := range rowBlocks {
		// Each block is sanBayH lines tall.  Wrap each line with the
		// cabinet rails and inner padding.
		for _, line := range strings.Split(block, "\n") {
			// Centre the row content within the inner cabinet width.
			centred := centre(line, inner)
			out.WriteString(padStr +
				chrome.Render("║") +
				centred +
				chrome.Render("║") + "\n")
		}
		// Inter-row vent strip between bay rows.
		if ri < len(rowBlocks)-1 {
			ventCount := inner / 4
			vent := strings.Repeat(steel.Render("·")+" "+frame.Render("·")+" ", ventCount)
			out.WriteString(padStr +
				chrome.Render("║") +
				pad(vent, inner) +
				chrome.Render("║") + "\n")
		}
	}

	// ── Status footer. ──────────────────────────────────────────────────
	done, active, queued, failed, skipped := 0, 0, 0, 0, 0
	for _, it := range s.items {
		switch it.Status {
		case sanDone:
			done++
		case sanActive:
			active++
		case sanQueued:
			queued++
		case sanFailed:
			failed++
		case sanSkipped:
			skipped++
		}
	}
	parts := []string{}
	parts = append(parts, mint.Render(fmt.Sprintf("%d/%d ARCHIVED", done, len(s.items))))
	if active > 0 {
		parts = append(parts, amber.Render(fmt.Sprintf("%d IN PROGRESS", active)))
	}
	if queued > 0 {
		parts = append(parts, steel.Render(fmt.Sprintf("%d QUEUED", queued)))
	}
	if failed > 0 {
		parts = append(parts, mag.Render(fmt.Sprintf("%d FAILED", failed)))
	}
	if skipped > 0 {
		parts = append(parts, steel.Render(fmt.Sprintf("%d SKIPPED", skipped)))
	}
	statusLine := steel.Render("STATUS: ") + strings.Join(parts, steel.Render(" · "))

	out.WriteString(padStr +
		chrome.Render("╠") +
		frame.Render(strings.Repeat("─", inner)) +
		chrome.Render("╣") + "\n")
	out.WriteString(padStr +
		chrome.Render("║ ") +
		pad(statusLine, inner-2) +
		chrome.Render(" ║") + "\n")

	// ── Bottom frame with bus port. ────────────────────────────────────
	half := (inner - 1) / 2
	out.WriteString(padStr +
		chrome.Render("╚") +
		chrome.Render(strings.Repeat("═", half)) +
		chrome.Render("╧") +
		chrome.Render(strings.Repeat("═", inner-half-1)) +
		chrome.Render("╝"))

	if indicator != "" {
		out.WriteString("\n" + indicator)
	}
	return out.String()
}

// composeCompact lays the bay grid on the screen without the chassis frame.
func (s san) composeCompact(rowBlocks []string, gridW int, indicator string) string {
	scenePad := (s.width - gridW) / 2
	if scenePad < 0 {
		scenePad = 0
	}
	padStr := strings.Repeat(" ", scenePad)
	var out strings.Builder
	for ri, block := range rowBlocks {
		for _, line := range strings.Split(block, "\n") {
			out.WriteString(padStr + line + "\n")
		}
		if ri < len(rowBlocks)-1 {
			out.WriteString("\n")
		}
	}
	if indicator != "" {
		out.WriteString("\n" + indicator)
	}
	return out.String()
}

func (s san) renderBay(it sanItem, isActive bool) []string {
	chromeC := fgStyle(colorSlate)
	frameC := fgStyle(colorSteel)
	steel := fgStyle(colorSteel)
	frost := fgBoldStyle(colorFrost)
	amber := fgBoldStyle(colorAmber)
	mint := fgBoldStyle(colorMint)
	mag := fgBoldStyle(colorMagenta)

	// State-driven palette + reel/LED.
	var reelCol, fillCol, ledCol, accent lipgloss.Color
	var statusTxt string
	statusSty := steel
	ledOn := false
	progress := 0.0
	switch it.Status {
	case sanQueued:
		reelCol, fillCol, ledCol, accent = colorSteel, colorSlate, colorSlate, colorSteel
		statusTxt = "QUEUED"
	case sanActive:
		reelCol, fillCol, ledCol, accent = colorPhosphor, colorPhosphor, colorAmber, colorAmber
		statusTxt = "ACTIVE"
		statusSty = amber
		ledOn = true
		progress = s.progress
	case sanDone:
		reelCol, fillCol, ledCol, accent = colorMint, colorMint, colorMint, colorMint
		statusTxt = "DONE"
		statusSty = mint
		ledOn = true
		progress = 1.0
	case sanFailed:
		reelCol, fillCol, ledCol, accent = colorMagenta, colorMagenta, colorMagenta, colorMagenta
		statusTxt = "FAILED"
		statusSty = mag
		ledOn = (s.frame/8)%2 == 0
	case sanSkipped:
		reelCol, fillCol, ledCol, accent = colorSlate, colorSlate, colorAmber, colorAmber
		statusTxt = "SKIPPED"
		statusSty = amber
		ledOn = (s.frame/8)%2 == 0
	}

	// Reel hub glyphs.
	hubGlyph := func(invert bool) string {
		switch it.Status {
		case sanQueued:
			return "◯"
		case sanDone:
			return "◉"
		case sanFailed, sanSkipped:
			return "◯"
		}
		// Active: rotate based on frame and progress speed.
		seq := []string{"◐", "◓", "◑", "◒"}
		ratio := 0.0
		if s.peak > 0 {
			ratio = math.Min(s.speed/s.peak, 1.0)
		}
		step := 6 - int(5*ratio)
		if step < 1 {
			step = 1
		}
		idx := (s.frame / step) % len(seq)
		if invert {
			idx = (len(seq) - 1 - idx + len(seq)) % len(seq)
		}
		return seq[idx]
	}

	hubL := fgBoldStyle(reelCol).Render(hubGlyph(false))
	hubR := fgBoldStyle(reelCol).Render(hubGlyph(true))

	pad := func(s string, w int) string {
		gap := w - lipgloss.Width(s)
		if gap < 0 {
			gap = 0
		}
		return s + strings.Repeat(" ", gap)
	}

	// Inner width (between │ rails).
	innerW := sanBayW - 2 // 20

	// ── Row 0: top with name plate "[LABEL ✓]". ─────────────────────────
	// Status conveyed by a single-cell glyph so longer file names can sit
	// inside the plate.  Available label width = innerW - 5  (1 dash +
	// "[" + " " + glyph + "]") = innerW - 5 = 15 cells at sanBayW=22.
	statusGlyph := map[sanItemStatus]string{
		sanQueued:  "◌",
		sanActive:  "▶",
		sanDone:    "✓",
		sanFailed:  "✗",
		sanSkipped: "⤳",
	}[it.Status]
	const dashLeft = 1
	plateGlyphCells := 1 // single rune
	plateLabelMax := innerW - dashLeft - 4 - plateGlyphCells
	if plateLabelMax < 1 {
		plateLabelMax = 1
	}
	plateLabel := truncateLabel(it.Label, plateLabelMax)
	plateStyled := fgStyle(accent).Render("[") +
		fgBoldStyle(colorFrost).Render(plateLabel) +
		" " + statusSty.Render(statusGlyph) +
		fgStyle(accent).Render("]")
	plateW := lipgloss.Width(plateStyled)
	rightDash := innerW - dashLeft - plateW
	if rightDash < 0 {
		rightDash = 0
	}
	r0 := chromeC.Render("┌") +
		frameC.Render(strings.Repeat("─", dashLeft)) +
		plateStyled +
		frameC.Render(strings.Repeat("─", rightDash)) +
		chromeC.Render("┐")

	// ── Rows 1-3: twin reel block (4-cell reels, side by side). ─────────
	// Reel layout per row: " ╭────╮  ╭────╮     "  — 6+2+6+5 padding = 20
	flange := frameC
	r1content := " " + flange.Render("╭────╮") + "  " + flange.Render("╭────╮") + "     "
	r2content := " " + flange.Render("│ ") + hubL + flange.Render("  │") + "  " +
		flange.Render("│ ") + hubR + flange.Render("  │") + "     "
	r3content := " " + flange.Render("╰────╯") + "  " + flange.Render("╰────╯") + "     "
	r1 := chromeC.Render("│") + pad(r1content, innerW) + chromeC.Render("│")
	r2 := chromeC.Render("│") + pad(r2content, innerW) + chromeC.Render("│")
	r3 := chromeC.Render("│") + pad(r3content, innerW) + chromeC.Render("│")

	// ── Row 4: fill bar + percentage.  innerW (20) = " "(1) + bar(13) +
	// " "(1) + " "(1) + pctStr(4) + " "(1) — keeps everything inside.
	const barW = 13
	filled := int(math.Round(progress * float64(barW)))
	if filled > barW {
		filled = barW
	}
	on := fgBoldStyle(fillCol)
	off := fgStyle(colorSlate)
	bar := on.Render(strings.Repeat("▓", filled)) +
		off.Render(strings.Repeat("░", barW-filled))
	pctStr := fmt.Sprintf("%3d%%", int(math.Round(progress*100)))
	pctSty := frost
	r4content := " " + bar + " " + pctSty.Render(pctStr) + " "
	r4 := chromeC.Render("│") + pad(r4content, innerW) + chromeC.Render("│")

	// ── Row 5: status LED + status word.  The label already lives in
	// the plate above, so this row carries the human-readable status
	// ("ACTIVE", "DONE", etc.) plus the LED.
	ledRune := "·"
	if ledOn {
		ledRune = "●"
	}
	led := fgBoldStyle(ledCol).Render(ledRune)
	r5content := " " + led + " " + statusSty.Render(statusTxt)
	r5 := chromeC.Render("│") + pad(r5content, innerW) + chromeC.Render("│")

	// ── Row 6: bottom border. ───────────────────────────────────────────
	r6 := chromeC.Render("└" + strings.Repeat("─", innerW) + "┘")

	return []string{r0, r1, r2, r3, r4, r5, r6}
}

// truncateLabel trims a string to at most max display cells, with an
// ellipsis when shortened.
func truncateLabel(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= max {
		return s
	}
	if max < 2 {
		return s[:max]
	}
	keep := max - 1
	return s[:keep] + "…"
}

// windowItems chooses [start,end) of items to show, centred on the active
// item where possible.
func (s san) windowItems(maxBays int) (int, int) {
	n := len(s.items)
	if n <= maxBays {
		return 0, n
	}
	start := s.active - maxBays/2
	if start < 0 {
		start = 0
	}
	end := start + maxBays
	if end > n {
		end = n
		start = end - maxBays
		if start < 0 {
			start = 0
		}
	}
	return start, end
}

func sanWindowIndicator(start, end, total, width int) string {
	steel := fgStyle(colorSteel)
	amber := fgBoldStyle(colorAmber)
	left := ""
	right := ""
	if start > 0 {
		left = amber.Render("◀") + steel.Render(fmt.Sprintf(" %d more ", start))
	}
	if end < total {
		right = steel.Render(fmt.Sprintf(" %d more ", total-end)) + amber.Render("▶")
	}
	mid := steel.Render(fmt.Sprintf(" showing %d–%d / %d ", start+1, end, total))
	line := left + mid + right
	pad := (width - lipgloss.Width(line)) / 2
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat(" ", pad) + line
}
