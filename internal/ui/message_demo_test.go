package ui

import (
	"testing"
)

// TestMessageBoxes demonstrates the styled message boxes.
func TestMessageBoxes(t *testing.T) {
	if testing.Short() {
		t.Skip("visual demo")
	}

	ShowMessage(MessageInfo, "DOWNLOAD SKIPPED", "File already exists: largefile.iso")

	ShowMessage(MessageWarning, "NO SAVED STATE", "Starting fresh download for: https://example.com/file.tar.gz")

	ShowMessage(MessageError, "RESUME FAILED", "Could not load saved state: file not found")

	ShowMessage(MessageSuccess, "STATE RECONSTRUCTED", "Recovered 8 part files — resuming download")
}

// TestResumePrompt demonstrates the animated resume prompt.
// Note: This test requires manual interaction and is skipped in CI.
func TestResumePrompt(t *testing.T) {
	if testing.Short() {
		t.Skip("visual demo - requires interaction")
	}

	// Simulate a partial download: 45MB out of 100MB
	downloaded := int64(45 * 1024 * 1024)
	total := int64(100 * 1024 * 1024)

	resume, err := ResumePrompt("ubuntu-24.04-live-server-amd64.iso", downloaded, total)
	if err != nil {
		t.Fatalf("ResumePrompt failed: %v", err)
	}

	if resume {
		t.Log("User chose to resume")
	} else {
		t.Log("User chose to start fresh")
	}
}
