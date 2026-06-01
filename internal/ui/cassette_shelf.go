package ui

import (
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// CassetteShelf renders a horizontal row of VHS tapes — one per URL in
// a yt-dlp batch.  Each cassette is drawn face-on (the way you'd see
// it on a video-rental shelf) with proper anatomy: chrome top/bottom
// edges, four corner screws, a white sticker label carrying the title,
// a transparent reel window with two rotating spools, a brand strip,
// and a footer chip with index / channel / runtime.
//
// At most one cassette is "active" — slightly lifted out of its slot
// (the animation), with chrome-bright borders, spinning reels and a
// pulsing status pill.  Idle tapes render the same skeleton in dimmer
// colours so the shelf reads as a continuous library rather than a
// row of identical boxes.
//
// Visual budget is **always 8 rows** regardless of detail tier so the
// VCR panel below never reflows.  When a cassette's detail tier drops
// (narrow terminal) we keep the chassis lines and pad the interior —
// the shelf height is fixed, not the content density.

const (
	// cassetteShelfRows is the total widget height *excluding* the
	// counter strip on top (added by View()).  Layout:
	//
	//   row 0..7 — cassette region (7-row tape body + 1 row of slot
	//              space that switches between top and bottom of the
	//              region depending on whether the cassette is lifted)
	//   row 8    — index numerals beneath each cassette
	cassetteShelfRows = 9
	cassetteBodyRows  = 7 // natural cassette height before lift positioning

	// Tier breakpoints, measured in per-cassette width (including border).
	cassetteTierLushW    = 14 // full anatomy: screws, label×2, info, reels, brand+footer
	cassetteTierMediumW  = 10 // title, chip+pill, reels, footer
	cassetteTierCompactW = 6  // pill + reels + index/runtime
)

// CassetteStatus is the lifecycle state of one tape on the shelf.
type CassetteStatus int

const (
	CassetteQueued CassetteStatus = iota
	CassetteProbing
	CassetteReady // probe done, waiting for the deck to swing to it
	CassetteLoading
	CassettePlaying
	CassetteMuxing
	CassetteDone
	CassetteSkipped
	CassetteFailed
)

// CassetteItem is one row of the shelf — everything renderable about
// a single URL in the batch.  Field renames cascade nowhere: the shelf
// is the only consumer.
type CassetteItem struct {
	URL        string
	Title      string        // resolved from yt-dlp metadata; empty until probed
	Channel    string        // populates the channel-tint sticker chip
	Duration   time.Duration // 00:00 until probed
	Resolution string        // "1080p60" — only on lush tier
	Status     CassetteStatus
	Err        string // populated on failure for the end-of-batch summary
}

// CassetteShelf is the widget.  Built once at batch start, mutated via
// the setter methods below as the orchestrator advances.
type CassetteShelf struct {
	items    []CassetteItem
	activeIx int // -1 before run, len(items) after
	frame    int

	// Animation springs — one per cassette so each can lift / settle
	// independently when it transitions to active.
	lift []float64 // current vertical offset (in cells, 0..1)
}

// NewCassetteShelf seeds the widget from a URL list.  Labels read as
// "tape NN" until probes resolve real titles.
func NewCassetteShelf(urls []string) *CassetteShelf {
	items := make([]CassetteItem, len(urls))
	for i, u := range urls {
		items[i] = CassetteItem{
			URL:    u,
			Status: CassetteQueued,
		}
	}
	return &CassetteShelf{
		items:    items,
		activeIx: -1,
		lift:     make([]float64, len(urls)),
	}
}

// Len reports how many tapes are on the shelf.
func (s *CassetteShelf) Len() int { return len(s.items) }

// Items returns a defensive copy so callers can iterate without racing
// concurrent mutations.  Used by the end-of-batch summary.
func (s *CassetteShelf) Items() []CassetteItem {
	out := make([]CassetteItem, len(s.items))
	copy(out, s.items)
	return out
}

// SetActive marks one cassette as the live tape.  Pass -1 to indicate
// no tape is currently in the deck (between items, or post-batch).
func (s *CassetteShelf) SetActive(i int) {
	if i < -1 || i >= len(s.items) {
		return
	}
	s.activeIx = i
}

// SetStatus updates one cassette's lifecycle state.  errMsg is recorded
// only when the new status is CassetteFailed.
func (s *CassetteShelf) SetStatus(i int, st CassetteStatus, errMsg string) {
	if i < 0 || i >= len(s.items) {
		return
	}
	s.items[i].Status = st
	if st == CassetteFailed {
		s.items[i].Err = errMsg
	}
}

// SetMeta fills in metadata that arrives from yt-dlp's probe.  Empty
// fields on the incoming item are ignored so partial probes don't blank
// out previously-set values.
func (s *CassetteShelf) SetMeta(i int, title, channel, resolution string, dur time.Duration) {
	if i < 0 || i >= len(s.items) {
		return
	}
	if title != "" {
		s.items[i].Title = title
	}
	if channel != "" {
		s.items[i].Channel = channel
	}
	if resolution != "" {
		s.items[i].Resolution = resolution
	}
	if dur > 0 {
		s.items[i].Duration = dur
	}
}

// Tick advances the lift animation for the active cassette and the
// reel-rotation frame counter.  Drives both the "tape pulls out of
// the shelf" effect and the spinning-reel illusion.
func (s *CassetteShelf) Tick() {
	s.frame++
	for i := range s.lift {
		target := 0.0
		if i == s.activeIx {
			target = 1.0
		}
		// Asymmetric easing: lifts fast, settles slow.  Matches the
		// physical feel of a tape being yanked then re-shelved.
		k := 0.18
		if s.lift[i] > target {
			k = 0.10
		}
		s.lift[i] += (target - s.lift[i]) * k
		if absF(s.lift[i]-target) < 0.01 {
			s.lift[i] = target
		}
	}
}

// View renders the shelf at the requested terminal width.  Returns at
// most cassetteShelfRows lines.  The layout is computed every call so
// resizes (WindowSizeMsg) reflow without ceremony.
func (s *CassetteShelf) View(termWidth int) string {
	if termWidth < 16 || len(s.items) == 0 {
		return ""
	}
	layout := planShelfLayout(termWidth, len(s.items), s.activeIx)
	rows := make([]strings.Builder, cassetteShelfRows)

	// Aggregate counter strip (above the cassettes).
	counter := s.renderCounter(termWidth)

	pad := strings.Repeat(" ", layout.LeftPad)

	// Render cassettes.  Cassette body height = cassetteShelfRows - 1
	// (last row reserved for the index strip beneath each spine).
	cassetteRows := make([][]string, layout.Count)
	tier := layout.Tier
	cw := layout.CassetteW
	for slot := 0; slot < layout.Count; slot++ {
		ix := layout.SlotIndex[slot]
		if ix == -1 {
			cassetteRows[slot] = renderOverflowStub(cw, layout.OverflowText[slot])
			continue
		}
		active := ix == s.activeIx
		liftCells := int(s.lift[ix] + 0.5)
		cassetteRows[slot] = renderCassette(s.items[ix], ix, active, liftCells, cw, tier, s.frame)
	}

	for r := 0; r < cassetteShelfRows-1; r++ {
		rows[r].WriteString(pad)
		for slot := 0; slot < layout.Count; slot++ {
			if slot > 0 {
				rows[r].WriteString(layout.Gap)
			}
			if r < len(cassetteRows[slot]) {
				rows[r].WriteString(cassetteRows[slot][r])
			} else {
				rows[r].WriteString(strings.Repeat(" ", cw))
			}
		}
	}

	// Last row: index numerals beneath each cassette (lush + medium + compact).
	if tier != cassetteTierSkinny {
		rows[cassetteShelfRows-1].WriteString(pad)
		for slot := 0; slot < layout.Count; slot++ {
			if slot > 0 {
				rows[cassetteShelfRows-1].WriteString(layout.Gap)
			}
			rows[cassetteShelfRows-1].WriteString(renderIndexLabel(layout.SlotIndex[slot], layout.OverflowText[slot], cw, s.activeIx))
		}
	}

	out := []string{counter}
	for _, r := range rows {
		out = append(out, r.String())
	}
	return strings.Join(out, "\n")
}

// renderCounter — single thin strip above the shelf summarising progress.
func (s *CassetteShelf) renderCounter(termWidth int) string {
	tally := struct{ done, fail, skip, queue, active int }{}
	for _, it := range s.items {
		switch it.Status {
		case CassetteDone:
			tally.done++
		case CassetteFailed:
			tally.fail++
		case CassetteSkipped:
			tally.skip++
		case CassettePlaying, CassetteMuxing, CassetteLoading, CassetteReady, CassetteProbing:
			tally.active++
		default:
			tally.queue++
		}
	}
	cur := s.activeIx + 1
	if cur < 1 {
		cur = 0
	}
	steel := fgStyle(Theme.Steel)
	mint := fgBoldStyle(Theme.Mint)
	mag := fgBoldStyle(Theme.Magenta)
	amber := fgBoldStyle(Theme.Amber)
	phos := fgBoldStyle(Theme.Phosphor)
	sep := steel.Render("  ·  ")

	parts := []string{
		phos.Render(fmt.Sprintf("⏵ %02d / %02d", cur, len(s.items))) + steel.Render(" tapes"),
	}
	if tally.done > 0 {
		parts = append(parts, mint.Render(fmt.Sprintf("✓ %d done", tally.done)))
	}
	if tally.fail > 0 {
		parts = append(parts, mag.Render(fmt.Sprintf("✗ %d failed", tally.fail)))
	}
	if tally.skip > 0 {
		parts = append(parts, steel.Render(fmt.Sprintf("− %d skipped", tally.skip)))
	}
	if tally.queue > 0 {
		parts = append(parts, amber.Render(fmt.Sprintf("⏸ %d queued", tally.queue)))
	}
	line := strings.Join(parts, sep)
	w := lipgloss.Width(line)
	pad := (termWidth - w) / 2
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat(" ", pad) + line
}

// ── Layout planner ──────────────────────────────────────────────────────────

type cassetteTier int

const (
	cassetteTierSkinny cassetteTier = iota
	cassetteTierCompact
	cassetteTierMedium
	cassetteTierLush
)

// shelfLayout describes the resolved geometry for one View() call.
type shelfLayout struct {
	CassetteW    int
	Count        int
	Tier         cassetteTier
	Gap          string
	LeftPad      int
	SlotIndex    []int    // item index for each slot, or -1 for overflow stubs
	OverflowText []string // for overflow stubs only — "+N more" / "+N done"
}

// planShelfLayout picks the cassette width and tier that best fit the
// available terminal width, then computes the visible window centred
// on the active item.
func planShelfLayout(termWidth, nItems, activeIx int) shelfLayout {
	avail := termWidth - 4
	if avail < 12 {
		avail = 12
	}

	// Pick the largest cnt (capped at min(6, nItems)) whose per-cassette
	// width clears the next non-skinny tier breakpoint.  Showing more
	// cassettes always beats showing one giant one, so we walk from
	// many → few and stop at the first viable candidate.
	maxCnt := nItems
	if maxCnt > 6 {
		maxCnt = 6
	}
	if maxCnt < 1 {
		maxCnt = 1
	}
	bestCount := 1
	bestW := avail
	bestGap := 2
	bestTier := cassetteTierSkinny
	for cnt := maxCnt; cnt >= 1; cnt-- {
		gap := 2
		w := (avail - gap*(cnt-1)) / cnt
		if w < 4 {
			continue
		}
		tier := cassetteTierForWidth(w)
		if tier > cassetteTierSkinny || cnt == 1 {
			bestCount = cnt
			bestW = w
			bestGap = gap
			bestTier = tier
			break
		}
	}

	used := bestW*bestCount + bestGap*(bestCount-1)
	leftPad := (avail - used) / 2
	if leftPad < 0 {
		leftPad = 0
	}
	leftPad += 2

	slotIx := make([]int, bestCount)
	overflow := make([]string, bestCount)
	start, end := windowAround(activeIx, bestCount, nItems)
	doneHidden := start
	moreHidden := nItems - end
	for slot := 0; slot < bestCount; slot++ {
		slotIx[slot] = start + slot
	}
	if doneHidden > 0 && bestCount >= 3 {
		slotIx[0] = -1
		overflow[0] = fmt.Sprintf("+%d", doneHidden)
	}
	if moreHidden > 0 && bestCount >= 3 {
		slotIx[bestCount-1] = -1
		overflow[bestCount-1] = fmt.Sprintf("+%d", moreHidden)
	}

	return shelfLayout{
		CassetteW:    bestW,
		Count:        bestCount,
		Tier:         bestTier,
		Gap:          strings.Repeat(" ", bestGap),
		LeftPad:      leftPad,
		SlotIndex:    slotIx,
		OverflowText: overflow,
	}
}

func cassetteTierForWidth(w int) cassetteTier {
	switch {
	case w >= cassetteTierLushW:
		return cassetteTierLush
	case w >= cassetteTierMediumW:
		return cassetteTierMedium
	case w >= cassetteTierCompactW:
		return cassetteTierCompact
	default:
		return cassetteTierSkinny
	}
}

func windowAround(active, slots, total int) (int, int) {
	if total <= slots {
		return 0, total
	}
	if active < 0 {
		return 0, slots
	}
	half := slots / 2
	start := active - half
	if start < 0 {
		start = 0
	}
	end := start + slots
	if end > total {
		end = total
		start = end - slots
	}
	return start, end
}

// ── Cassette rendering ──────────────────────────────────────────────────────

// renderCassette returns exactly cassetteShelfRows-1 strings forming
// one cassette region.  The cassette body itself is cassetteBodyRows
// tall; the extra row is a "slot space" that appears above (when the
// tape is at rest in its slot) or below (when the tape is lifted out).
// This way the lift animation never crops the cassette's borders.
func renderCassette(it CassetteItem, index int, active bool, liftCells, width int, tier cassetteTier, frame int) []string {
	var body []string
	switch tier {
	case cassetteTierLush:
		body = cassetteLushBody(it, index, active, frame, width)
	case cassetteTierMedium:
		body = cassetteMediumBody(it, index, active, frame, width)
	case cassetteTierCompact:
		body = cassetteCompactBody(it, index, active, frame, width)
	default:
		return cassetteSkinnyBody(it, index, active, liftCells, width, frame)
	}
	return positionInSlot(body, liftCells, width)
}

// positionInSlot places a cassette body within its 8-row slot region.
// Lifted (liftCells > 0): body occupies the TOP of the slot, blank
// row sits at the bottom — visually the tape has been pulled UP out
// of its sleeve.  At rest: body occupies the BOTTOM of the slot, blank
// row sits at the top — the tape is fully seated.
func positionInSlot(body []string, liftCells, width int) []string {
	slotRows := cassetteShelfRows - 1 // 8
	blank := strings.Repeat(" ", width)
	out := make([]string, slotRows)
	if liftCells > 0 {
		// Lifted: body at top.
		for i := 0; i < slotRows; i++ {
			if i < len(body) {
				out[i] = body[i]
			} else {
				out[i] = blank
			}
		}
	} else {
		// At rest: body at bottom, blank above.
		offset := slotRows - len(body)
		if offset < 0 {
			offset = 0
		}
		for i := 0; i < slotRows; i++ {
			if i < offset {
				out[i] = blank
			} else {
				bi := i - offset
				if bi < len(body) {
					out[i] = body[bi]
				} else {
					out[i] = blank
				}
			}
		}
	}
	return out
}

// cassetteLushBody draws a recognisable VHS face — top edge with
// screws, white sticker label (2 lines), info row (chip + status pill
// + resolution badge), reel window, brand-strip-with-footer combined,
// bottom edge with screws.  Exactly 7 rows.  Width ≥ 14.
//
//	┌─□────────□─┐
//	│ Linux Kern │     ← sticker label line 1
//	│ 6.8 Releas │     ← sticker label line 2
//	│ ▓CHN▓  [▶] │     ← info row: chip + status + resolution
//	│ ◜◠◝━━━━◞◡◟ │     ← reel window with magnetic tape strip
//	│ TDK №03 432│     ← brand + index + runtime
//	└─□────────□─┘
func cassetteLushBody(it CassetteItem, index int, active bool, frame, w int) []string {
	pal := resolveCassettePalette(it.Status, active, frame)
	inner := w - 2
	if inner < 8 {
		inner = 8
	}
	edge := cassetteEdge(inner, pal.Border)

	// Title sticker: leading space + content + trailing pad, clamped
	// to exactly `inner` cells so the cream background stops at the
	// border instead of bleeding past it.
	titleA, titleB := splitTitle(displayTitle(it, index), inner-2)
	stickerStyle := lipgloss.NewStyle().Foreground(pal.LabelFG).Background(pal.LabelBG)
	lineA := stickerStyle.Render(clampWidth(" "+titleA, inner))
	lineB := stickerStyle.Render(clampWidth(" "+titleB, inner))

	pill := statusPill(it.Status, active, frame)
	chip := channelChip(it.Channel)
	res := resolutionBadge(it.Resolution, pal.Dim)
	// Pill first (status is non-negotiable), then chip, then resolution.
	// infoRow drops trailing parts that don't fit.
	chipRow := infoRow(inner, pill, chip, res)

	reels := renderReels(it.Status, active, frame, inner)

	// Combined brand + footer row.  Brand chip sits flush left, the
	// index/runtime sit flush right.  Dropped fields when budget tight.
	footer := lushFooterRow(it, index, inner, pal)

	side := pal.Border.Render("│")
	return []string{
		edge.top,
		side + lineA + side,
		side + lineB + side,
		side + chipRow + side,
		side + reels + side,
		side + footer + side,
		edge.bottom,
	}
}

// cassetteMediumBody — same chassis, one title line, chip + pill, reels,
// runtime footer.  Exactly 7 rows.  Width 10..13.
//
//	┌─□──────□─┐
//	│ Linux Kr │     ← title sticker (1 line)
//	│ ▓CHN▓ [▶]│     ← chip + status
//	│ ◜◠◝──◞◡◟│     ← reels
//	│  ▒ TDK ▒ │     ← brand strip
//	│№03·04:32 │     ← runtime footer
//	└─□──────□─┘
func cassetteMediumBody(it CassetteItem, index int, active bool, frame, w int) []string {
	pal := resolveCassettePalette(it.Status, active, frame)
	inner := w - 2
	if inner < 6 {
		inner = 6
	}
	edge := cassetteEdge(inner, pal.Border)

	titleLine, _ := splitTitle(displayTitle(it, index), inner-2)
	stickerStyle := lipgloss.NewStyle().Foreground(pal.LabelFG).Background(pal.LabelBG)
	titleRow := stickerStyle.Render(clampWidth(" "+titleLine, inner))

	pill := statusPill(it.Status, active, frame)
	chip := channelChip(it.Channel)
	res := resolutionBadge(it.Resolution, pal.Dim)
	chipRow := infoRow(inner, pill, chip, res)

	reels := renderReels(it.Status, active, frame, inner)
	brand := brandStrip(it.URL, inner)
	footer := mediumFooterRow(it, index, inner, pal)

	side := pal.Border.Render("│")
	return []string{
		edge.top,
		side + titleRow + side,
		side + chipRow + side,
		side + reels + side,
		side + brand + side,
		side + footer + side,
		edge.bottom,
	}
}

// cassetteCompactBody — minimal but still recognisably a tape.  width
// 6..9.  Drops the title sticker entirely; the cassette face shows the
// reels + status pill + index/runtime stamp.  Exactly 7 rows.
//
//	┌─□──□─┐
//	│ [▶] │
//	│      │
//	│◜◠◝◞◡◟│
//	│      │
//	│№03·4:32│
//	└─□──□─┘
func cassetteCompactBody(it CassetteItem, index int, active bool, frame, w int) []string {
	pal := resolveCassettePalette(it.Status, active, frame)
	inner := w - 2
	if inner < 4 {
		inner = 4
	}
	edge := cassetteEdge(inner, pal.Border)

	pill := statusPill(it.Status, active, frame)
	pillRow := centreCell(pill, inner)

	reels := renderReels(it.Status, active, frame, inner)
	footer := compactFooterRow(it, index, inner, pal)

	side := pal.Border.Render("│")
	return []string{
		edge.top,
		side + pillRow + side,
		side + padInner("", inner) + side,
		side + reels + side,
		side + padInner("", inner) + side,
		side + footer + side,
		edge.bottom,
	}
}

// cassetteSkinnyBody — no border, just status glyph + reel column +
// index numeral.  Used at very narrow widths where any drawn chassis
// would consume the whole budget.  Returns slot-positioned 8 rows.
func cassetteSkinnyBody(it CassetteItem, index int, active bool, liftCells, w, frame int) []string {
	pal := resolveCassettePalette(it.Status, active, frame)
	pill := statusPill(it.Status, active, frame)
	num := pal.Dim.Render(fmt.Sprintf("%02d", index+1))
	body := []string{
		centreCell(pill, w),
		"",
		centreCell(pal.Body.Render("┃"), w),
		centreCell(pal.Body.Render("┃"), w),
		"",
		centreCell(num, w),
		"",
	}
	return positionInSlot(body, liftCells, w)
}

// ── Component pieces ────────────────────────────────────────────────────────

type cassetteEdges struct{ top, bottom string }

// cassetteEdge — top + bottom chrome strips with screw holes at the corners.
// On narrow cassettes the screws sit closer to the corners; on wide ones
// they're indented two cells in.
func cassetteEdge(inner int, border lipgloss.Style) cassetteEdges {
	// Inner edge: dash run with screw glyphs (□) at fixed positions.
	if inner < 4 {
		bar := strings.Repeat("─", inner)
		return cassetteEdges{
			top:    border.Render("┌" + bar + "┐"),
			bottom: border.Render("└" + bar + "┘"),
		}
	}
	mid := strings.Repeat("─", inner-4)
	top := border.Render("┌─□" + mid + "□─┐")
	bottom := border.Render("└─□" + mid + "□─┘")
	return cassetteEdges{top: top, bottom: bottom}
}

// renderReels — the iconic VHS reel pair, framed by the cassette's
// transparent window.  Real VHS reels sit roughly a third of the way
// in from each side, not edge-to-edge — so we cap the magnetic-tape
// strip between them at ~half the cassette inner width and centre
// the whole group, leaving padded "window glass" on either side.
//
// When the tape is active, the reels spin (take-up slightly faster
// than supply).  Done tapes settle to a hub glyph; failed tapes get
// broken-spoke X's.
func renderReels(st CassetteStatus, active bool, frame, inner int) string {
	body := fgStyle(Theme.Frost)
	if !active {
		body = fgStyle(Theme.Steel)
	}
	switch st {
	case CassetteFailed:
		body = fgBoldStyle(Theme.Magenta)
	case CassetteDone:
		body = fgBoldStyle(Theme.Mint)
	case CassetteSkipped:
		body = fgStyle(Theme.Slate)
	}

	// Branch on available width.  Each reel glyph is 3 cells wide, so
	// we need ≥ 6 inner cells just to fit them edge-to-edge.  Below
	// that we collapse to single-cell hub glyphs.
	if inner >= 6 {
		left, right := reelFrames(st, active, frame)
		// Magnetic-tape strip between the reels.  Capped so wide
		// cassettes don't stretch the reels apart.
		tapeLen := inner - 6 // 6 = two 3-cell reels
		if tapeLen < 1 {
			tapeLen = 1
		}
		if tapeLen > 14 {
			tapeLen = 14
		}
		if active && (st == CassettePlaying || st == CassetteMuxing) {
			shades := []string{"━", "─"}
			var sb strings.Builder
			for i := 0; i < tapeLen; i++ {
				sb.WriteString(shades[(i+frame/3)%len(shades)])
			}
			mag := fgStyle(Theme.Magenta).Render(sb.String())
			core := body.Render(left) + mag + body.Render(right)
			return centreCell(core, inner)
		}
		tape := fgStyle(Theme.Slate).Render(strings.Repeat("─", tapeLen))
		core := body.Render(left) + tape + body.Render(right)
		return centreCell(core, inner)
	}

	// Narrow: 1-char hub glyphs with a thin tape strip.
	hub := reelHubGlyph(st, active, frame)
	tapeLen := inner - 2
	if tapeLen < 1 {
		tapeLen = 1
	}
	tape := fgStyle(Theme.Slate).Render(strings.Repeat("─", tapeLen))
	core := body.Render(hub) + tape + body.Render(hub)
	return centreCell(core, inner)
}

// reelHubGlyph picks a single-cell reel glyph used at very narrow
// cassette widths where the full 3-cell reel pair won't fit.
func reelHubGlyph(st CassetteStatus, active bool, frame int) string {
	switch st {
	case CassetteFailed:
		return "╳"
	case CassetteDone:
		return "◉"
	case CassetteSkipped:
		return "◯"
	}
	if active && (st == CassettePlaying || st == CassetteMuxing || st == CassetteLoading) {
		frames := []string{"◐", "◓", "◑", "◒"}
		return frames[(frame/4)%4]
	}
	return "◯"
}

// reelFrames picks the two reel glyphs based on lifecycle + frame.
// Active tapes get rotating frames at slightly different speeds to
// sell the illusion of magnetic tape transport.
func reelFrames(st CassetteStatus, active bool, frame int) (string, string) {
	frames := []string{"◜◠◝", "◝◠◞", "◞◡◟", "◟◡◜"}
	switch st {
	case CassetteFailed:
		return "╳·╳", "╳·╳"
	case CassetteDone:
		return "◉◠◉", "◉◠◉"
	case CassetteSkipped:
		return "◯◯◯", "◯◯◯"
	}
	if active && (st == CassettePlaying || st == CassetteMuxing || st == CassetteLoading) {
		l := frames[(frame/4)%4]
		r := frames[(frame/3)%4]
		return l, r
	}
	if st == CassetteProbing || st == CassetteReady {
		// Gentle idle wobble — half speed, single phase.
		f := frames[(frame/14)%4]
		return f, f
	}
	return "◯◯◯", "◯◯◯"
}

// statusPill — bracketed status readout like a tiny LCD on the cassette.
func statusPill(st CassetteStatus, active bool, frame int) string {
	glyph, col := cassetteStatusGlyph(st, frame)
	if active && (st == CassettePlaying || st == CassetteMuxing) {
		// Pulsing background pill — chassis "RECORDING" indicator.
		bg := Theme.Magenta
		if (frame/10)%2 == 0 {
			bg = Theme.Phosphor
		}
		return lipgloss.NewStyle().
			Foreground(Theme.Frost).
			Background(bg).
			Bold(true).
			Render(" " + glyph + " ")
	}
	pillStyle := lipgloss.NewStyle().
		Foreground(col).
		Bold(true)
	return pillStyle.Render("[" + glyph + "]")
}

// brandStrip — bottom-of-cassette brand chip.  Picks the longest
// brand variant that fits the inner width, falling back to the short
// brandLabel on narrow cassettes, and finally to a plain chevron
// pair when even that won't fit.
func brandStrip(url string, inner int) string {
	long := []string{
		"TDK·SHG", "MAXELL XL", "SONY E180", "BASF·E",
		"JVC·SHG", "FUJI·H", "AMPEX 196", "HGET·VHS",
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(url))
	pickLong := long[int(h.Sum32())%len(long)]
	pickShort := brandLabel(url)
	steel := fgStyle(Theme.Steel)
	slate := fgStyle(Theme.Slate)
	chevrons := slate.Render("▒")
	tryBrand := func(name string) (string, bool) {
		core := chevrons + " " + steel.Render(name) + " " + chevrons
		w := lipgloss.Width(core)
		if w <= inner {
			return centreCell(core, inner), true
		}
		return "", false
	}
	if s, ok := tryBrand(pickLong); ok {
		return s
	}
	if s, ok := tryBrand(pickShort); ok {
		return s
	}
	// Fallback: just chevrons.
	return centreCell(slate.Render("▒ ▒"), inner)
}

// lushFooterRow — left-anchored brand strip + right-anchored index/runtime.
// Picks the widest layout that fits the cassette inner width.
func lushFooterRow(it CassetteItem, index, inner int, pal cassettePalette) string {
	idx := fmt.Sprintf("№ %02d", index+1)
	rt := formatHMSShort(it.Duration)
	right := pal.Dim.Render(idx) + pal.Body.Render(" · ") + pal.Dim.Render(rt)
	rw := lipgloss.Width(right)
	left := pal.Dim.Render("▒ ") + pal.Body.Render(brandLabel(it.URL)) + pal.Dim.Render(" ▒")
	lw := lipgloss.Width(left)
	// Need at least 2 cells (leading + trailing space) + a 1-cell gap.
	if lw+rw+3 <= inner {
		gap := inner - lw - rw - 2
		if gap < 1 {
			gap = 1
		}
		return " " + left + strings.Repeat(" ", gap) + right + " "
	}
	// Fallback: just centre the index/runtime.
	return centreCell(right, inner)
}

// mediumFooterRow — index + runtime, dropping resolution when tight.
func mediumFooterRow(it CassetteItem, index, inner int, pal cassettePalette) string {
	idx := fmt.Sprintf("№%02d", index+1)
	rt := formatHMSShort(it.Duration)
	core := pal.Dim.Render(idx) + pal.Body.Render(" ") + pal.Dim.Render(rt)
	if lipgloss.Width(core) > inner {
		core = pal.Dim.Render(rt)
	}
	if lipgloss.Width(core) > inner {
		core = pal.Dim.Render(idx)
	}
	return centreCell(core, inner)
}

// compactFooterRow — index OR runtime, whichever fits.
func compactFooterRow(it CassetteItem, index, inner int, pal cassettePalette) string {
	idx := fmt.Sprintf("№%02d", index+1)
	rt := formatHMSShort(it.Duration)
	if lipgloss.Width(idx)+lipgloss.Width(rt)+1 <= inner {
		return centreCell(pal.Dim.Render(idx+" "+rt), inner)
	}
	if lipgloss.Width(rt) <= inner {
		return centreCell(pal.Dim.Render(rt), inner)
	}
	return centreCell(pal.Dim.Render(idx), inner)
}

// brandLabel returns the brand string used in the footer strip.
// Deterministic per-URL so the same video always wears the same brand.
func brandLabel(url string) string {
	brands := []string{"TDK", "MAXELL", "SONY", "BASF", "JVC", "FUJI", "AMPEX", "HGET"}
	h := fnv.New32a()
	_, _ = h.Write([]byte(url))
	return brands[int(h.Sum32())%len(brands)]
}

// infoRow lays out one or more rendered fragments on a single
// cassette-interior row, left-anchored with 2-space gaps.  When the
// combined width would exceed `inner`, later fragments are silently
// dropped (callers therefore pass the most important fragment first).
// Always padded to exactly `inner` cells so the chassis stays aligned.
func infoRow(inner int, parts ...string) string {
	var keep []string
	used := 1 // leading space
	for _, p := range parts {
		w := lipgloss.Width(p)
		if w == 0 {
			continue
		}
		sep := 0
		if len(keep) > 0 {
			sep = 2
		}
		if used+sep+w > inner {
			continue
		}
		keep = append(keep, p)
		used += sep + w
	}
	line := " " + strings.Join(keep, "  ")
	pad := inner - lipgloss.Width(line)
	if pad < 0 {
		return line
	}
	return line + strings.Repeat(" ", pad)
}

// resolutionBadge — small inset chip showing "1080p", "4K", "720p"
// etc.  Hidden when no resolution is known.
func resolutionBadge(resolution string, dim lipgloss.Style) string {
	if resolution == "" {
		return ""
	}
	compact := compactResolution(resolution)
	return lipgloss.NewStyle().
		Foreground(Theme.Frost).
		Background(Theme.Slate).
		Render(" " + compact + " ")
}

// compactResolution turns "1920x1080" into "1080p", "3840x2160" into
// "4K", etc.  Standard ladder; falls back to the raw string when
// the input isn't a recognised resolution.
func compactResolution(s string) string {
	if s == "" {
		return ""
	}
	parts := strings.Split(s, "x")
	if len(parts) != 2 {
		return s
	}
	var h int
	_, err := fmt.Sscanf(parts[1], "%d", &h)
	if err != nil {
		return s
	}
	switch {
	case h >= 4320:
		return "8K"
	case h >= 2160:
		return "4K"
	case h >= 1440:
		return "1440p"
	case h >= 1080:
		return "1080p"
	case h >= 720:
		return "720p"
	case h >= 480:
		return "480p"
	case h >= 360:
		return "360p"
	default:
		return fmt.Sprintf("%dp", h)
	}
}

// renderOverflowStub — the "+N" filler that takes one slot when the
// shelf can't fit every cassette.  Rendered as a stack of dim
// chevrons so it doesn't compete with real cassettes for attention.
func renderOverflowStub(width int, text string) []string {
	dim := fgStyle(Theme.Slate)
	steel := fgStyle(Theme.Steel)
	rows := make([]string, cassetteShelfRows-1)
	for i := range rows {
		rows[i] = strings.Repeat(" ", width)
	}
	mid := (cassetteShelfRows - 1) / 2
	if width >= 4 {
		rows[mid-1] = centreCell(dim.Render("┃"), width)
		rows[mid] = centreCell(steel.Render(text), width)
		rows[mid+1] = centreCell(dim.Render("┃"), width)
		rows[mid+2] = centreCell(dim.Render("·"), width)
	}
	return rows
}

func renderIndexLabel(ix int, overflow string, width, activeIx int) string {
	if ix == -1 {
		return centreCell(fgStyle(Theme.Slate).Render(overflow), width)
	}
	label := fmt.Sprintf("%02d", ix+1)
	if ix == activeIx {
		return centreCell(fgBoldStyle(Theme.Magenta).Render("▶ "+label), width)
	}
	return centreCell(fgStyle(Theme.Steel).Render(label), width)
}

// channelChip — small coloured rectangle hashed from channel name.
// 6 distinct hues keyed so the same channel always lands on the same
// colour across runs (the library is "organised").
func channelChip(channel string) string {
	palette := []lipgloss.Color{
		Theme.Magenta, Theme.Amber, Theme.Mint,
		Theme.Phosphor, Theme.DeepCyan, Theme.Frost,
	}
	if channel == "" {
		return fgStyle(Theme.Slate).Render("░·░")
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(channel))
	col := palette[int(h.Sum32())%len(palette)]
	abbr := channelAbbr(channel)
	return lipgloss.NewStyle().Foreground(Theme.Frost).Background(col).Bold(true).Render(" " + abbr + " ")
}

func channelAbbr(s string) string {
	r := []rune(strings.ToUpper(strings.TrimSpace(s)))
	if len(r) == 0 {
		return "···"
	}
	if len(r) >= 3 {
		return string(r[:3])
	}
	for len(r) < 3 {
		r = append(r, '·')
	}
	return string(r)
}

// cassetteStatusGlyph picks the lifecycle glyph + its base colour.
func cassetteStatusGlyph(st CassetteStatus, frame int) (string, lipgloss.Color) {
	switch st {
	case CassetteQueued:
		return "⏸", Theme.Steel
	case CassetteProbing:
		frames := []string{"◌", "◍", "◎", "●"}
		return frames[(frame/8)%4], Theme.Amber
	case CassetteReady:
		return "◐", Theme.Amber
	case CassetteLoading:
		frames := []string{"◐", "◓", "◑", "◒"}
		return frames[(frame/6)%4], Theme.Amber
	case CassettePlaying:
		return "▶", Theme.Magenta
	case CassetteMuxing:
		return "✦", Theme.Phosphor
	case CassetteDone:
		return "✓", Theme.Mint
	case CassetteSkipped:
		return "−", Theme.Slate
	case CassetteFailed:
		return "✗", Theme.Magenta
	}
	return " ", Theme.Steel
}

// cassettePalette resolves the colour bundle used to render one
// cassette in a given state.  Active tapes always use a chrome-bright
// border + cream label so they pop off the shelf regardless of status.
type cassettePalette struct {
	Border  lipgloss.Style
	Body    lipgloss.Style
	BodyBG  lipgloss.Style
	Dim     lipgloss.Style
	LabelFG lipgloss.Color
	LabelBG lipgloss.Color
}

func resolveCassettePalette(st CassetteStatus, active bool, frame int) cassettePalette {
	// Sticker label colours — cream over a darker cardboard tint so it
	// reads as a real handwritten library label even when the tape is
	// idle.  Active tapes brighten the cream toward white.
	labelFG := Theme.Steel
	labelBG := lipgloss.Color("#1A1F2E")
	switch st {
	case CassetteDone:
		labelFG = Theme.Mint
		labelBG = lipgloss.Color("#0E2018")
	case CassetteFailed:
		labelFG = Theme.Magenta
		labelBG = lipgloss.Color("#2A1218")
	case CassetteSkipped:
		labelFG = Theme.Slate
	case CassetteReady, CassetteLoading, CassetteProbing:
		labelFG = Theme.Amber
	case CassettePlaying, CassetteMuxing:
		labelFG = Theme.Frost
		labelBG = lipgloss.Color("#2A1218")
	}

	pal := cassettePalette{
		Border:  fgStyle(Theme.Steel),
		Body:    fgStyle(Theme.Steel),
		Dim:     fgStyle(Theme.Slate),
		LabelFG: labelFG,
		LabelBG: labelBG,
		BodyBG:  lipgloss.NewStyle(),
	}
	switch st {
	case CassetteDone:
		pal.Border = fgStyle(Theme.Mint)
		pal.Body = fgStyle(Theme.Mint)
	case CassetteFailed:
		pal.Border = fgStyle(Theme.Magenta)
		pal.Body = fgBoldStyle(Theme.Magenta)
	case CassetteSkipped:
		pal.Border = fgStyle(Theme.Slate)
		pal.Body = fgStyle(Theme.Slate)
		pal.Dim = fgStyle(Theme.Slate)
	case CassettePlaying, CassetteMuxing:
		pal.Border = fgBoldStyle(Theme.Magenta)
		pal.Body = fgBoldStyle(Theme.Frost)
	case CassetteReady, CassetteLoading, CassetteProbing:
		pal.Border = fgStyle(Theme.Amber)
		pal.Body = fgStyle(Theme.Amber)
	}
	if active {
		pal.Border = fgBoldStyle(Theme.Frost)
		// Pulse the label background a touch toward magenta on the
		// active tape — reads like the "REC" LED bleeding onto the
		// label from inside the deck.
		if (frame/12)%2 == 0 {
			pal.LabelBG = lipgloss.Color("#2A1218")
		}
	}
	return pal
}

// ── Tiny helpers ────────────────────────────────────────────────────────────

func padInner(s string, w int) string {
	gap := w - lipgloss.Width(s)
	if gap < 0 {
		return s
	}
	return s + strings.Repeat(" ", gap)
}

// clampWidth pads a string to exactly w cells OR truncates it with an
// ellipsis when too long.  Unlike padInner, content longer than w
// gets shortened — used for the sticker label so background styling
// never extends beyond the cassette border.
func clampWidth(s string, w int) string {
	cw := lipgloss.Width(s)
	if cw == w {
		return s
	}
	if cw < w {
		return s + strings.Repeat(" ", w-cw)
	}
	return truncate(s, w)
}

func centreCell(s string, w int) string {
	gap := w - lipgloss.Width(s)
	if gap <= 0 {
		return s
	}
	l := gap / 2
	r := gap - l
	return strings.Repeat(" ", l) + s + strings.Repeat(" ", r)
}

func splitTitle(title string, maxWidth int) (string, string) {
	if title == "" || maxWidth <= 0 {
		return "", ""
	}
	if len(title) <= maxWidth {
		return title, ""
	}
	words := strings.Fields(title)
	var a, b strings.Builder
	spilled := false
	for _, w := range words {
		if !spilled {
			if a.Len() == 0 {
				a.WriteString(w)
				continue
			}
			if a.Len()+1+len(w) <= maxWidth {
				a.WriteByte(' ')
				a.WriteString(w)
				continue
			}
			spilled = true
		}
		if b.Len() == 0 {
			b.WriteString(w)
			continue
		}
		if b.Len()+1+len(w) <= maxWidth {
			b.WriteByte(' ')
			b.WriteString(w)
		}
	}
	la := a.String()
	lb := b.String()
	if lb == "" && len(la) > maxWidth {
		lb = la[maxWidth:]
		la = la[:maxWidth]
		if len(lb) > maxWidth {
			lb = lb[:maxWidth-1] + "…"
		}
	}
	if len(la) > maxWidth {
		la = truncate(la, maxWidth)
	}
	if len(lb) > maxWidth {
		lb = truncate(lb, maxWidth)
	}
	return la, lb
}

// displayTitle returns the title to print on the cassette's sticker
// label.  Falls back to a placeholder when the probe hasn't landed.
func displayTitle(it CassetteItem, index int) string {
	if it.Title != "" {
		return it.Title
	}
	switch it.Status {
	case CassetteProbing:
		return "scanning…"
	case CassetteFailed:
		return "probe failed"
	}
	return fmt.Sprintf("tape %02d", index+1)
}

func formatHMSShort(d time.Duration) string {
	if d <= 0 {
		return "--:--"
	}
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

// applyLift shifts every row up by liftCells, padding the bottom with
// blank rows so the cassette occupies exactly `height` lines regardless
// of how high it's lifted.
func applyLift(rows []string, liftCells, height, width int) []string {
	if liftCells <= 0 {
		if len(rows) >= height {
			return rows[:height]
		}
		blank := strings.Repeat(" ", width)
		for len(rows) < height {
			rows = append(rows, blank)
		}
		return rows
	}
	blank := strings.Repeat(" ", width)
	out := make([]string, height)
	for i := 0; i < height; i++ {
		src := i + liftCells
		if src < len(rows) {
			out[i] = rows[src]
		} else {
			out[i] = blank
		}
	}
	return out
}

func absF(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
