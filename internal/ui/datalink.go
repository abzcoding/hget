package ui

// datalink.go — vintage rack-mount data-link / modem visualisation.
//
// A dial-up modem with blinking status LEDs is the iconic physical
// artefact of "downloading" — the way a cassette is for music playback.
// The panel maps directly onto an HTTP downloader:
//
//   • PWR / CD / TX / RX / OH / AA   — status LEDs flicker with activity
//   • per-channel rows               — one per parallel connection
//   • aggregate signal bar           — total throughput meter
//
// The whole download view collapses into this single panel: no duplicated
// per-part rows, no separate overall bar — every piece of telemetry has
// one canonical home.

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// ── LEDs ──────────────────────────────────────────────────────────────────────

// led models a single front-panel indicator with a brightness that decays
// over time.  Activity "ignites" the LED, then it fades — exactly like a
// real LED with a phosphor afterglow.
type led struct {
	brightness float64
	pulse      float64
}

func (l *led) decay(rate float64) {
	l.brightness -= rate
	if l.brightness < 0 {
		l.brightness = 0
	}
}

func (l *led) ignite(target float64) {
	if target > l.brightness {
		l.brightness = target
	}
}

// render returns a single ANSI-coloured "●" glyph whose colour reflects
// brightness.  Glyph width never changes so column alignment is preserved.
func (l led) render(activeColor, dimColor lipgloss.Color) string {
	switch {
	case l.brightness >= 0.7:
		return lipgloss.NewStyle().Foreground(activeColor).Bold(true).Render("●")
	case l.brightness >= 0.25:
		return lipgloss.NewStyle().Foreground(activeColor).Render("●")
	default:
		return lipgloss.NewStyle().Foreground(dimColor).Render("●")
	}
}

// ── data-link model ───────────────────────────────────────────────────────────

type dataLink struct {
	pwr, cd, tx, rx, oh, aa led
	// per-channel activity LEDs (2 per channel, randomly flickering)
	chanLEDs [][2]led
	rng      *rand.Rand
	// rolling tick counter — used to pace TX / AA blinks
	ticks int
}

func newDataLink() dataLink {
	return dataLink{
		pwr: led{brightness: 1},
		cd:  led{brightness: 1},
		oh:  led{brightness: 1},
		aa:  led{brightness: 0.7},
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Tick advances the LED animation.
//   totalSpeed  bytes/s — drives RX brightness
//   peak        bytes/s — normalisation reference
//   partSpeeds  per-channel bytes/s — drives per-channel activity LEDs
func (d *dataLink) Tick(totalSpeed, peak float64, partSpeeds []float64) {
	d.ticks++

	// Always-on LEDs hold steady (small decay + re-ignite).
	const standbyDecay = 0.005
	d.pwr.decay(standbyDecay)
	d.cd.decay(standbyDecay)
	d.oh.decay(standbyDecay)
	d.aa.decay(standbyDecay)
	d.pwr.ignite(1.0)
	d.cd.ignite(1.0)
	d.oh.ignite(1.0)
	// AA pulses gently every ~1.6 s.
	if d.ticks%100 == 0 {
		d.aa.ignite(0.9)
	}
	d.aa.ignite(0.6)

	// TX / RX flicker with activity.
	const flickerDecay = 0.08
	d.tx.decay(flickerDecay)
	d.rx.decay(flickerDecay)

	if peak > 0 && totalSpeed > 0 {
		ratio := math.Min(totalSpeed/peak, 1.0)
		// RX is the workhorse — brightness follows current rate.
		d.rx.ignite(0.35 + 0.65*math.Sqrt(ratio))
		// TX pulses sporadically — outgoing requests are sparse but persistent.
		if d.rng.Float64() < 0.08+0.15*ratio {
			d.tx.ignite(0.9)
		}
	}

	// Per-channel activity LEDs.
	if len(d.chanLEDs) != len(partSpeeds) {
		d.chanLEDs = make([][2]led, len(partSpeeds))
	}
	for i, sp := range partSpeeds {
		d.chanLEDs[i][0].decay(flickerDecay * 1.5)
		d.chanLEDs[i][1].decay(flickerDecay * 1.5)
		if peak > 0 && sp > 0 {
			ratio := math.Min(sp/peak, 1.0)
			if d.rng.Float64() < 0.15+0.55*ratio {
				idx := d.rng.Intn(2)
				d.chanLEDs[i][idx].ignite(0.95)
			}
		}
	}
}

// ── geometry ──────────────────────────────────────────────────────────────────

const (
	dataLinkInnerW = 70
	channelBarCells = 22
	aggBarCells     = 30
)

// ── rendering ─────────────────────────────────────────────────────────────────

// channelRow describes the data passed in for one parallel connection.
type channelRow struct {
	Index      int
	Pct        float64 // 0..1 displayed (spring-smoothed)
	RawPct     float64 // 0..1 actual
	Speed      float64 // bytes/s
	Done       bool
	HasStarted bool
}

// View renders the full data-link panel.
//
//   channels   — per-connection rows
//   totalDown  — aggregate bytes transferred
//   size       — total bytes (0 if unknown)
//   peak       — peak observed total speed (for ETA / peak readout)
//   elapsed    — duration since transfer started
//   status     — short status word ("downloading" / "stopping" / …)
//   carrier    — true when the link is "up" (drives header colour)
func (d *dataLink) View(
	channels []channelRow,
	totalDown, size int64,
	peak float64,
	elapsed time.Duration,
	status string,
	carrier bool,
) string {
	// ── shell ─────────────────────────────────────────────────────────────
	shellStyle := lipgloss.NewStyle().Foreground(colorPhosphor)
	railStyle := styleSep

	// Status-specific LED patterns and colors
	var statusColor lipgloss.Color
	var pwrOn, cdOn, txOn, rxOn, ohOn, aaOn bool
	
	switch status {
	case "HANDSHAKE":
		statusColor = colorCyan
		pwrOn, cdOn = true, true
		// TX/RX blink during handshake
		txOn = d.ticks%4 < 2
		rxOn = d.ticks%4 >= 2
	case "DOWNLOADING":
		statusColor = colorMint
		pwrOn, cdOn, txOn, rxOn = true, true, true, true
	case "COMPLETE":
		statusColor = colorMint
		pwrOn, cdOn = true, true
		// All LEDs solid on completion
		txOn, rxOn, aaOn = true, true, true
	case "STOPPING", "SKIPPING":
		statusColor = colorAmber
		pwrOn, cdOn = true, true
		// OH (overload/halt) blinks
		ohOn = d.ticks%3 < 2
	case "ERROR":
		statusColor = colorMagenta
		pwrOn = true
		// OH solid, others off
		ohOn = true
	default:
		statusColor = colorPhosphor
		pwrOn = true
	}
	
	// Override LED brightness based on state flags
	if pwrOn {
		d.pwr.ignite(1.0)
	} else {
		d.pwr.brightness = 0.1
	}
	if cdOn {
		d.cd.ignite(1.0)
	} else {
		d.cd.brightness = 0.1
	}
	if txOn {
		d.tx.ignite(0.9)
	} else {
		d.tx.brightness = 0.1
	}
	if rxOn {
		d.rx.ignite(0.9)
	} else {
		d.rx.brightness = 0.1
	}
	if ohOn {
		d.oh.ignite(0.95)
	} else {
		d.oh.brightness = 0.1
	}
	if aaOn {
		d.aa.ignite(0.9)
	} else {
		d.aa.brightness = 0.3
	}
	
	if !carrier && status != "COMPLETE" && status != "ERROR" {
		statusColor = colorPhosphor
	}

	headerLabel := lipgloss.NewStyle().Foreground(colorPhosphor).Bold(true).Render("▣ HGET")
	headerKind := lipgloss.NewStyle().Foreground(colorSteel).Render(" · DATA LINK · ")
	headerStatus := lipgloss.NewStyle().Foreground(statusColor).Bold(true).Render(strings.ToUpper(status))
	headerInner := headerLabel + headerKind + headerStatus
	headerInnerW := lipgloss.Width(headerInner)
	dashesAvail := dataLinkInnerW - headerInnerW - 4 // account for "─ " bookends + side rails-aware padding
	if dashesAvail < 4 {
		dashesAvail = 4
	}
	leftDash := dashesAvail / 2
	rightDash := dashesAvail - leftDash
	top := shellStyle.Render("╭─") +
		railStyle.Render(strings.Repeat("─", leftDash)) +
		" " + headerInner + " " +
		railStyle.Render(strings.Repeat("─", rightDash)) +
		shellStyle.Render("─╮")
	bot := shellStyle.Render("╰─" + strings.Repeat("─", dataLinkInnerW-2) + "─╯")
	side := shellStyle.Render("│")

	// Helper: wrap inner content padded to dataLinkInnerW cells.
	wrap := func(content string) string {
		pad := dataLinkInnerW - lipgloss.Width(content)
		if pad < 0 {
			pad = 0
		}
		return side + content + strings.Repeat(" ", pad) + side
	}
	// Helper: centred row.
	wrapCenter := func(content string) string {
		w := lipgloss.Width(content)
		spaces := dataLinkInnerW - w
		if spaces < 0 {
			spaces = 0
		}
		left := spaces / 2
		right := spaces - left
		return side + strings.Repeat(" ", left) + content + strings.Repeat(" ", right) + side
	}

	// ── LED row ───────────────────────────────────────────────────────────
	ledLabel := func(name string, l led, on, off lipgloss.Color) string {
		lab := lipgloss.NewStyle().Foreground(colorSteel).Render(name)
		return lab + " " + l.render(on, off)
	}
	ledRow := strings.Join([]string{
		ledLabel("PWR", d.pwr, colorMint, colorSlate),
		ledLabel("CD", d.cd, colorMint, colorSlate),
		ledLabel("TX", d.tx, colorAmber, colorSlate),
		ledLabel("RX", d.rx, colorAmber, colorSlate),
		ledLabel("OH", d.oh, colorMint, colorSlate),
		ledLabel("AA", d.aa, colorPhosphor, colorSlate),
	}, "   ")

	// ── thin rule between LEDs and channels ──────────────────────────────
	rule := railStyle.Render(strings.Repeat("┄", dataLinkInnerW-4))

	// ── channel rows ─────────────────────────────────────────────────────
	channelLines := make([]string, 0, len(channels))
	for i, ch := range channels {
		line := d.renderChannelRow(ch, i)
		channelLines = append(channelLines, line)
	}

	// ── aggregate row ────────────────────────────────────────────────────
	aggregateLines := d.renderAggregate(totalDown, size, peak, elapsed)

	// ── stitch ───────────────────────────────────────────────────────────
	var b strings.Builder
	b.WriteString(top + "\n")
	b.WriteString(wrapCenter(ledRow) + "\n")
	b.WriteString(wrapCenter(rule) + "\n")
	for _, ln := range channelLines {
		b.WriteString(wrap(ln) + "\n")
	}
	b.WriteString(wrapCenter(rule) + "\n")
	for _, ln := range aggregateLines {
		b.WriteString(wrap(ln) + "\n")
	}
	b.WriteString(bot)
	return b.String()
}

// renderChannelRow renders a single per-connection line:
//
//   ▸ CH·01  ┃▰▰▰▰▰▰▰▰▰▰▰▱▱▱▱▱▱▱▱▱▱▱┃   72.3%    ↓ 1.2 MB/s   ●●
func (d *dataLink) renderChannelRow(ch channelRow, idx int) string {
	bullet := lipgloss.NewStyle().Foreground(colorAmber).Render("▸")
	label := lipgloss.NewStyle().Foreground(colorSteel).Render(fmt.Sprintf("CH·%02d", ch.Index+1))

	// Bar.
	bar := renderChannelBar(ch.Pct, ch.Done)

	pct := math.Max(0, math.Min(ch.RawPct, 1.0))
	pctTxt := fmt.Sprintf("%5.1f%%", pct*100)
	pctStyled := lipgloss.NewStyle().Foreground(colorFrost).Render(pctTxt)

	// Render speed in a fixed 13-cell column so activity LEDs always align.
	const speedW = 13
	pad := func(s string, w int) string {
		gap := w - lipgloss.Width(s)
		if gap < 0 {
			gap = 0
		}
		return s + strings.Repeat(" ", gap)
	}
	var speedTxt string
	switch {
	case ch.Done:
		speedTxt = pad(lipgloss.NewStyle().Foreground(colorMint).Bold(true).Render("done"), speedW)
	case ch.Speed > 0:
		speedTxt = pad(lipgloss.NewStyle().Foreground(colorAmber).Render(formatSpeed(ch.Speed)), speedW)
	default:
		speedTxt = strings.Repeat(" ", speedW)
	}

	// Per-channel activity LEDs (2 dots).
	var act string
	if idx < len(d.chanLEDs) {
		act = d.chanLEDs[idx][0].render(colorPhosphor, colorSlate) +
			d.chanLEDs[idx][1].render(colorPhosphor, colorSlate)
	} else {
		act = lipgloss.NewStyle().Foreground(colorSlate).Render("●●")
	}

	return "  " + bullet + " " + label + "  " +
		lipgloss.NewStyle().Foreground(colorPhosphor).Render("┃") +
		bar +
		lipgloss.NewStyle().Foreground(colorPhosphor).Render("┃") +
		"  " + pctStyled + "   " + speedTxt + "   " + act
}

// renderAggregate renders the total / aggregate readout (3 lines):
//
//   ◆ LINK    ┃▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▱▱┃   73.8%
//             ↓ 4.8 MB/s   ETA 02:14   peak 6.2 MB/s
func (d *dataLink) renderAggregate(totalDown, size int64, peak float64, elapsed time.Duration) []string {
	pct := 0.0
	if size > 0 {
		pct = math.Min(float64(totalDown)/float64(size), 1.0)
	}

	bullet := lipgloss.NewStyle().Foreground(colorAmber).Bold(true).Render("◆")
	label := lipgloss.NewStyle().Foreground(colorAmber).Bold(true).Render("LINK ")
	bar := renderAggregateBar(pct)

	pctTxt := lipgloss.NewStyle().Foreground(colorFrost).Bold(true).
		Render(fmt.Sprintf("%5.1f%%", pct*100))

	row1 := "  " + bullet + " " + label + "  " +
		lipgloss.NewStyle().Foreground(colorPhosphor).Render("┃") +
		bar +
		lipgloss.NewStyle().Foreground(colorPhosphor).Render("┃") +
		"  " + pctTxt

	// Stats line (ETA, current rate, peak).
	currentSpeed := 0.0
	if elapsed.Seconds() > 0 {
		currentSpeed = float64(totalDown) / elapsed.Seconds()
	}
	parts := []string{}
	if currentSpeed > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorAmber).Bold(true).
			Render(formatSpeed(currentSpeed)))
	}
	if pct > 0.001 && size > 0 {
		eta := elapsed.Seconds()/pct - elapsed.Seconds()
		if eta > 0 {
			parts = append(parts, lipgloss.NewStyle().Foreground(colorSteel).Render("ETA ")+
				lipgloss.NewStyle().Foreground(colorPhosphor).Render(formatDuration(time.Duration(eta*float64(time.Second)))))
		}
	}
	if peak > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorSteel).Render("peak ")+
			lipgloss.NewStyle().Foreground(colorPhosphor).Render(formatSpeed(peak)))
	}
	row2 := strings.Repeat(" ", 12) + strings.Join(parts, lipgloss.NewStyle().Foreground(colorSlate).Render("   ·   "))
	return []string{row1, row2}
}

// renderChannelBar renders a 22-cell signal bar coloured per state.
func renderChannelBar(pct float64, done bool) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 1 {
		pct = 1
	}
	filled := int(math.Round(pct * float64(channelBarCells)))
	if filled > channelBarCells {
		filled = channelBarCells
	}
	on := lipgloss.NewStyle().Foreground(colorPhosphor)
	off := lipgloss.NewStyle().Foreground(colorSlate)
	if done {
		on = lipgloss.NewStyle().Foreground(colorMint)
	}
	return on.Render(strings.Repeat("▰", filled)) +
		off.Render(strings.Repeat("▱", channelBarCells-filled))
}

// renderAggregateBar — a wider 30-cell bar with a phosphor→amber gradient
// near the right edge to "light up" as the transfer nears completion.
func renderAggregateBar(pct float64) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 1 {
		pct = 1
	}
	filled := int(math.Round(pct * float64(aggBarCells)))
	if filled > aggBarCells {
		filled = aggBarCells
	}
	// Last 3 filled cells fade to amber.
	body := ""
	threshold := filled - 3
	if threshold < 0 {
		threshold = 0
	}
	body += lipgloss.NewStyle().Foreground(colorPhosphor).Render(strings.Repeat("▰", threshold))
	if filled > threshold {
		body += lipgloss.NewStyle().Foreground(colorAmber).Bold(true).Render(strings.Repeat("▰", filled-threshold))
	}
	body += lipgloss.NewStyle().Foreground(colorSlate).Render(strings.Repeat("▱", aggBarCells-filled))
	return body
}
