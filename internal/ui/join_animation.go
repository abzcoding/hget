package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// joinAnimation renders an animated file assembly visualization.
type joinAnimation struct {
	frame int
}

func newJoinAnimation() joinAnimation {
	return joinAnimation{}
}

func (j *joinAnimation) Tick() {
	j.frame++
}

func (j joinAnimation) View(pct float64, current, total int) string {
	cAmber := Theme.Amber
	cMint := Theme.Mint
	cSteel := Theme.Steel
	cSlate := Theme.Slate

	styleBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cAmber).
		Padding(1, 2).
		Width(66)

	styleLabel := lipgloss.NewStyle().Foreground(cSteel)
	styleValue := lipgloss.NewStyle().Foreground(cAmber).Bold(true)
	styleDone := lipgloss.NewStyle().Foreground(cMint).Bold(true)
	styleDim := lipgloss.NewStyle().Foreground(cSlate)

	// Animated assembly visualization
	// Show parts being assembled with moving arrows
	const numBlocks = 8
	blocks := make([]string, numBlocks)

	completedBlocks := int(pct * float64(numBlocks))
	activeBlock := j.frame % numBlocks

	for i := 0; i < numBlocks; i++ {
		if i < completedBlocks {
			blocks[i] = styleDone.Render("▰")
		} else if i == activeBlock && completedBlocks < numBlocks {
			// Animated assembly indicator
			chars := []string{"◐", "◓", "◑", "◒"}
			blocks[i] = styleValue.Render(chars[j.frame%len(chars)])
		} else {
			blocks[i] = styleDim.Render("▱")
		}
	}

	assembly := strings.Join(blocks, " ")

	// Status line with animated arrows
	var arrows string
	if pct < 1.0 {
		arrowChars := []string{"→", "⇒", "⟹"}
		arrows = styleValue.Render(arrowChars[j.frame%len(arrowChars)])
	} else {
		arrows = styleDone.Render("✓")
	}

	statusLine := fmt.Sprintf("%s  %s  %s",
		arrows,
		styleValue.Render("ASSEMBLING"),
		styleDim.Render(fmt.Sprintf("%d/%d parts", current, total)),
	)

	pctLine := fmt.Sprintf("%s %s",
		styleLabel.Render("progress"),
		styleValue.Render(fmt.Sprintf("%.1f%%", pct*100)),
	)

	content := fmt.Sprintf("%s\n\n%s\n\n%s",
		statusLine,
		assembly,
		pctLine,
	)

	return styleBox.Render(content)
}
