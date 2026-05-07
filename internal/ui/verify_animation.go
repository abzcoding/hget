package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// verifyAnimation renders an animated GPG signature verification.
type verifyAnimation struct {
	frame int
}

func newVerifyAnimation() verifyAnimation {
	return verifyAnimation{}
}

func (v *verifyAnimation) Tick() {
	v.frame++
}

func (v verifyAnimation) View() string {
	cPhosphor := lipgloss.Color("#73E0FF")
	cSteel := lipgloss.Color("#5A6B85")
	cSlate := lipgloss.Color("#3A475C")

	styleBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cPhosphor).
		Padding(1, 2).
		Width(66)

	styleLabel := lipgloss.NewStyle().Foreground(cSteel)
	styleValue := lipgloss.NewStyle().Foreground(cPhosphor).Bold(true)
	styleDim := lipgloss.NewStyle().Foreground(cSlate)

	// Animated key/lock visualization
	keyChars := []string{"🔑", "🗝️"}
	lockChars := []string{"🔒", "🔓"}

	keyIdx := (v.frame / 3) % len(keyChars)
	lockIdx := (v.frame / 3) % len(lockChars)

	key := keyChars[keyIdx]
	lock := lockChars[lockIdx]

	// Scanning animation
	const scanWidth = 20
	scanPos := v.frame % (scanWidth * 2)
	if scanPos >= scanWidth {
		scanPos = scanWidth*2 - scanPos - 1
	}

	scanLine := strings.Repeat(" ", scanPos) +
		styleValue.Render("▓") +
		strings.Repeat(" ", scanWidth-scanPos-1)

	// Status with animated dots
	dots := strings.Repeat(".", (v.frame/5)%4)
	status := fmt.Sprintf("%s  %s%s",
		styleValue.Render("VERIFYING GPG SIGNATURE"),
		styleDim.Render(dots),
		strings.Repeat(" ", 3-len(dots)),
	)

	visual := fmt.Sprintf("%s %s %s",
		key,
		styleDim.Render("→"),
		lock,
	)

	content := fmt.Sprintf("%s\n\n%s\n\n%s %s",
		status,
		visual,
		styleLabel.Render("scan"),
		scanLine,
	)

	return styleBox.Render(content)
}
