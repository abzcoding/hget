package ui

import (
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// ResumePrompt shows an animated TUI prompt asking whether to resume a partial download.
// Returns true if user wants to resume, false to start fresh.
func ResumePrompt(taskName string, downloaded, total int64) (bool, error) {
	cPhosphor := lipgloss.Color("#73E0FF")
	cAmber := lipgloss.Color("#FFB75A")
	cMint := lipgloss.Color("#5EE6A1")
	cSteel := lipgloss.Color("#5A6B85")

	theme := huh.ThemeCharm()
	theme.Focused.Base = theme.Focused.Base.BorderForeground(cAmber)
	theme.Focused.Title = theme.Focused.Title.Foreground(cPhosphor).Bold(true)
	theme.Focused.Description = theme.Focused.Description.Foreground(cSteel)
	theme.Focused.SelectedOption = theme.Focused.SelectedOption.Foreground(cMint)
	theme.Blurred.Base = theme.Blurred.Base.BorderForeground(cSteel)

	var resume bool

	pct := float64(downloaded) / float64(total) * 100
	description := fmt.Sprintf("Found partial download: %s (%.1f%% complete)\nResume from where you left off?",
		formatBytes(downloaded), pct)

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Key("resume").
				Title("◆ RESUME DOWNLOAD").
				Description(description).
				Affirmative("Resume").
				Negative("Start Fresh").
				Value(&resume),
		),
	).WithTheme(theme)

	if err := form.Run(); err != nil {
		return false, err
	}

	return resume, nil
}
