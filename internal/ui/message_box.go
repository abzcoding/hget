package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// MessageBox renders a styled message box that fits the modem theme.
type MessageType int

const (
	MessageInfo MessageType = iota
	MessageWarning
	MessageError
	MessageSuccess
)

// ShowMessage displays a styled message box and returns immediately.
func ShowMessage(msgType MessageType, title, message string) {
	cPhosphor := Theme.Phosphor
	cAmber := Theme.Amber
	cMint := Theme.Mint
	cMagenta := Theme.Magenta
	cSteel := Theme.Steel

	var borderColor lipgloss.Color
	var icon string
	var titleColor lipgloss.Color

	switch msgType {
	case MessageInfo:
		borderColor = cPhosphor
		icon = "◆"
		titleColor = cPhosphor
	case MessageWarning:
		borderColor = cAmber
		icon = "⚠"
		titleColor = cAmber
	case MessageError:
		borderColor = cMagenta
		icon = "◈"
		titleColor = cMagenta
	case MessageSuccess:
		borderColor = cMint
		icon = "⬢"
		titleColor = cMint
	}

	styleBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 2).
		Width(66)

	styleTitle := lipgloss.NewStyle().Foreground(titleColor).Bold(true)
	styleMessage := lipgloss.NewStyle().Foreground(cSteel)

	titleLine := fmt.Sprintf("%s  %s", icon, styleTitle.Render(title))

	// Wrap message to fit box width
	maxWidth := 60
	lines := strings.Split(message, "\n")
	wrappedLines := make([]string, 0)
	for _, line := range lines {
		if len(line) <= maxWidth {
			wrappedLines = append(wrappedLines, line)
		} else {
			// Simple word wrap
			words := strings.Fields(line)
			current := ""
			for _, word := range words {
				if len(current)+len(word)+1 <= maxWidth {
					if current != "" {
						current += " "
					}
					current += word
				} else {
					if current != "" {
						wrappedLines = append(wrappedLines, current)
					}
					current = word
				}
			}
			if current != "" {
				wrappedLines = append(wrappedLines, current)
			}
		}
	}

	content := titleLine + "\n\n" + styleMessage.Render(strings.Join(wrappedLines, "\n"))

	fmt.Println()
	fmt.Println(styleBox.Render(content))
	fmt.Println()
}
