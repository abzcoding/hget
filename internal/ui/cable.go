package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// cableState mirrors the upstream / downstream pipe state.
type cableState int

const (
	cableIdle         cableState = iota // pre-handshake — sparse blip
	cableConnecting                     // handshake — blips climbing
	cableActive                         // active transfer — packet stream
	cableComplete                       // transfer ok — solid steady
	cableDisconnected                   // skip/error — broken line, red
)

// cable models the bus connecting two endpoints.
type cable struct {
	frame int
	// packetPos is a fractional row index in [0..rows).  Each Tick advances
	// it by `velocity` so animation speed scales with bandwidth.
	packetPos float64
	velocity  float64 // rows/frame
	state     cableState
}

func newCable() cable {
	return cable{velocity: 0.25}
}

// Tick advances the packet animation.  rateRatio (0..1) is the current
// throughput normalised against peak; higher rates make packets travel
// faster, reinforcing the visceral feedback.
func (c *cable) Tick(rateRatio float64) {
	c.frame++
	if rateRatio < 0 {
		rateRatio = 0
	}
	if rateRatio > 1 {
		rateRatio = 1
	}
	// 0.15..0.55 rows/frame — snappy enough to read at 60 fps.
	c.velocity = 0.15 + 0.4*rateRatio
	c.packetPos += c.velocity
}

// SetState updates the cable's behavioural mode.
func (c *cable) SetState(s cableState) {
	c.state = s
	if s == cableDisconnected || s == cableComplete {
		c.velocity = 0
	}
}

func (c cable) View(rows, width, portCol int) string {
	if rows < 1 {
		rows = 1
	}
	if portCol < 0 || portCol >= width {
		portCol = width / 2
	}

	chrome := fgStyle(colorPhosphor)
	dim := fgStyle(colorSlate)

	var trunkColor lipgloss.Color
	var packetColor lipgloss.Color
	var trunkRune, brokenRune string
	switch c.state {
	case cableIdle:
		trunkColor = colorSlate
		packetColor = colorSteel
		trunkRune = "│"
	case cableConnecting:
		trunkColor = colorAmber
		packetColor = colorAmber
		trunkRune = "│"
	case cableActive:
		trunkColor = colorPhosphor
		packetColor = colorAmber
		trunkRune = "║"
	case cableComplete:
		trunkColor = colorMint
		packetColor = colorMint
		trunkRune = "║"
	case cableDisconnected:
		trunkColor = colorMagenta
		packetColor = colorMagenta
		trunkRune = "╴"
		brokenRune = " "
	}

	trunkSty := fgStyle(trunkColor)
	pktSty := fgBoldStyle(packetColor)

	// Compute packet row index modulo rows.
	pkt := int(c.packetPos) % rows
	if pkt < 0 {
		pkt += rows
	}

	// For idle/connecting, blink an amber blip every few frames.
	showPacket := true
	switch c.state {
	case cableIdle:
		showPacket = (c.frame/8)%3 == 0
	case cableConnecting:
		showPacket = (c.frame/4)%2 == 0
	case cableDisconnected:
		// Two red endpoint dots blink; no traveling packet.
		showPacket = false
	case cableComplete:
		// Steady solid — no traveling packet.
		showPacket = false
	}

	var b strings.Builder
	for r := 0; r < rows; r++ {
		// Build base row: spaces, with trunk glyph at portCol.
		left := strings.Repeat(" ", portCol)
		right := strings.Repeat(" ", width-portCol-1)

		// For disconnected state, alternate trunk/blank to give a torn look.
		ch := trunkRune
		if c.state == cableDisconnected {
			if r%2 == 1 {
				ch = brokenRune
			}
		}
		var glyph string
		switch {
		case showPacket && r == pkt:
			glyph = pktSty.Render("●")
		case ch == " ":
			glyph = " "
		default:
			glyph = trunkSty.Render(ch)
		}
		b.WriteString(left + glyph + right)
		if r < rows-1 {
			b.WriteString("\n")
		}
	}

	// Suppress unused-warning for chrome/dim helpers we may want for
	// branching variants (kept for symmetry with mainframe.go).
	_ = chrome
	_ = dim
	return b.String()
}

func (c cable) ViewBranching(width, portCol int, dropCols []int, activeDrop int) string {
	if width < 3 || len(dropCols) == 0 {
		return strings.Repeat(" ", width)
	}

	chromeOn := fgStyle(colorPhosphor)
	chromeDim := fgStyle(colorSlate)
	pkt := fgBoldStyle(colorAmber)
	mint := fgStyle(colorMint)
	mag := fgStyle(colorMagenta)

	// pick line palette per state
	lineSty := chromeOn
	switch c.state {
	case cableIdle, cableConnecting:
		lineSty = chromeDim
	case cableComplete:
		lineSty = mint
	case cableDisconnected:
		lineSty = mag
	}

	// Row 0: vertical trunk segment under the port.
	// Row 1: horizontal bus connecting all drop columns; intersection at portCol.
	// Row 2: vertical drop legs.
	// Row 3: drop terminator (▼) at each dropCol; the active one carries a
	//        traveling packet pulse.

	row0 := make([]rune, width)
	row1 := make([]rune, width)
	row2 := make([]rune, width)
	row3 := make([]rune, width)
	for i := range row0 {
		row0[i], row1[i], row2[i], row3[i] = ' ', ' ', ' ', ' '
	}

	// Trunk segment.
	if portCol >= 0 && portCol < width {
		row0[portCol] = '║'
	}

	// Determine bus span.
	minC, maxC := portCol, portCol
	for _, d := range dropCols {
		if d < minC {
			minC = d
		}
		if d > maxC {
			maxC = d
		}
	}
	if minC < 0 {
		minC = 0
	}
	if maxC >= width {
		maxC = width - 1
	}
	for i := minC; i <= maxC; i++ {
		row1[i] = '═'
	}
	// Corners / tees.
	if portCol >= minC && portCol <= maxC {
		switch {
		case portCol == minC && portCol == maxC:
			row1[portCol] = '║' // single drop
		case portCol == minC:
			row1[portCol] = '╔'
		case portCol == maxC:
			row1[portCol] = '╗'
		default:
			row1[portCol] = '╦'
		}
	}
	for _, d := range dropCols {
		if d < 0 || d >= width {
			continue
		}
		switch {
		case d == minC && d != portCol:
			row1[d] = '╔'
		case d == maxC && d != portCol:
			row1[d] = '╗'
		case d != portCol:
			row1[d] = '╦'
		}
		row2[d] = '║'
		row3[d] = '▼'
	}

	// Render with style; highlight the active drop column.
	render := func(r []rune, isDropRow bool) string {
		var b strings.Builder
		for i, ch := range r {
			if ch == ' ' {
				b.WriteByte(' ')
				continue
			}
			styled := lineSty.Render(string(ch))
			if isDropRow && activeDrop >= 0 && activeDrop < len(dropCols) && i == dropCols[activeDrop] {
				switch c.state {
				case cableComplete:
					styled = mint.Render(string(ch))
				case cableDisconnected:
					styled = mag.Render(string(ch))
				default:
					styled = pkt.Render(string(ch))
				}
			}
			b.WriteString(styled)
		}
		return b.String()
	}

	return render(row0, false) + "\n" +
		render(row1, false) + "\n" +
		render(row2, true) + "\n" +
		render(row3, true)
}
