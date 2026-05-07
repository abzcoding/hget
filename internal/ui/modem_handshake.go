package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// modemHandshake renders an animated dial-up modem connection sequence.
type modemHandshake struct {
	frame int
	phase int // 0=dialing, 1=carrier detect, 2=handshake, 3=connected
}

func newModemHandshake() modemHandshake {
	return modemHandshake{}
}

func (m *modemHandshake) Tick() {
	m.frame++
	// Advance phase every ~15 frames
	if m.frame%15 == 0 && m.phase < 3 {
		m.phase++
	}
}

func (m modemHandshake) View(url string) string {
	cPhosphor := lipgloss.Color("#73E0FF")
	cAmber := lipgloss.Color("#FFB75A")
	cMint := lipgloss.Color("#5EE6A1")
	cSteel := lipgloss.Color("#5A6B85")
	cSlate := lipgloss.Color("#3A475C")

	styleBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cSteel).
		Padding(1, 2).
		Width(66)

	styleLabel := lipgloss.NewStyle().Foreground(cSteel)
	styleValue := lipgloss.NewStyle().Foreground(cPhosphor).Bold(true)
	styleDim := lipgloss.NewStyle().Foreground(cSlate)

	var status, indicator string
	var ledColor lipgloss.Color

	switch m.phase {
	case 0: // Dialing
		status = "DIALING"
		ledColor = cAmber
		// Rotating dial animation
		chars := []string{"◜", "◝", "◞", "◟"}
		indicator = chars[m.frame%len(chars)]
	case 1: // Carrier detect
		status = "CARRIER DETECT"
		ledColor = cAmber
		// Pulsing dots
		dots := (m.frame % 4) + 1
		indicator = strings.Repeat("●", dots) + strings.Repeat("○", 4-dots)
	case 2: // Handshake
		status = "HANDSHAKE"
		ledColor = cPhosphor
		// Alternating arrows
		if m.frame%4 < 2 {
			indicator = "↑↓"
		} else {
			indicator = "↓↑"
		}
	case 3: // Connected
		status = "LINK ESTABLISHED"
		ledColor = cMint
		indicator = "●●"
	}

	led := lipgloss.NewStyle().Foreground(ledColor).Bold(true).Render("●")
	statusLine := fmt.Sprintf("%s  %s  %s",
		led,
		styleValue.Render(status),
		styleDim.Render(indicator),
	)

	// Truncate URL if too long
	displayURL := url
	if len(displayURL) > 60 {
		displayURL = displayURL[:57] + "..."
	}

	content := fmt.Sprintf("%s\n\n%s %s",
		statusLine,
		styleLabel.Render("target"),
		styleDim.Render(displayURL),
	)

	return styleBox.Render(content)
}
