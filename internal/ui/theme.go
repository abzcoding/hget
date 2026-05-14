// Package ui — theme.go

package ui

import "github.com/charmbracelet/lipgloss"

type theme struct {
	Phosphor lipgloss.Color // dominant cyan
	Amber    lipgloss.Color // sharp accent
	Mint     lipgloss.Color // success
	Magenta  lipgloss.Color // error
	Steel    lipgloss.Color // chrome / labels
	Slate    lipgloss.Color // dim chrome / separators
	Frost    lipgloss.Color // highlight values
	DeepCyan lipgloss.Color // bar gradient start
}

var Theme = theme{
	Phosphor: lipgloss.Color("#73E0FF"),
	Amber:    lipgloss.Color("#FFB75A"),
	Mint:     lipgloss.Color("#5EE6A1"),
	Magenta:  lipgloss.Color("#FF5478"),
	Steel:    lipgloss.Color("#5A6B85"),
	Slate:    lipgloss.Color("#3A475C"),
	Frost:    lipgloss.Color("#E8F1F8"),
	DeepCyan: lipgloss.Color("#1E7A99"),
}

// Box renders a chrome-bordered panel in the carrier style.  Used by the
// modem, mainframe, tape, vault, and message-box primitives so they share
// identical chrome.
func ThemeBox(border lipgloss.Color, width int) lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Padding(1, 2).
		Width(width)
}
