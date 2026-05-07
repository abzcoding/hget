package ui

import (
	"fmt"
	"testing"
)

// TestModemHandshakeAnimation demonstrates the modem handshake phases.
func TestModemHandshakeAnimation(t *testing.T) {
	if testing.Short() {
		t.Skip("visual demo")
	}
	
	modem := newModemHandshake()
	url := "https://example.com/large-file.iso"
	
	phases := []struct {
		name   string
		frames int
	}{
		{"DIALING", 15},
		{"CARRIER DETECT", 15},
		{"HANDSHAKE", 15},
		{"LINK ESTABLISHED", 10},
	}
	
	for _, phase := range phases {
		fmt.Printf("\n─── %s ───\n", phase.name)
		for i := 0; i < phase.frames; i++ {
			modem.Tick()
		}
		fmt.Println(modem.View(url))
	}
}

// TestSkipAnimation demonstrates red blinking LEDs during skip.
func TestSkipAnimation(t *testing.T) {
	if testing.Short() {
		t.Skip("visual demo")
	}
	
	d := newDataLink()
	
	channels := []channelRow{
		{Index: 1, Pct: 0.45, RawPct: 0.45, Speed: 0, HasStarted: true},
		{Index: 2, Pct: 0.32, RawPct: 0.32, Speed: 0, HasStarted: true},
		{Index: 3, Pct: 0.51, RawPct: 0.51, Speed: 0, HasStarted: true},
		{Index: 4, Pct: 0.38, RawPct: 0.38, Speed: 0, HasStarted: true},
	}
	
	fmt.Println("\n─── SKIPPING (red blinking LEDs) ───")
	
	// Tick a few times to show the blinking animation
	for i := 0; i < 8; i++ {
		d.Tick(0, 5_900_000, []float64{0, 0, 0, 0})
	}
	
	fmt.Println(d.View(channels, 45_000_000, 100_000_000, 5_900_000, 0, "SKIPPING", false))
}

// TestJoinAnimation demonstrates the file assembly animation.
func TestJoinAnimation(t *testing.T) {
	if testing.Short() {
		t.Skip("visual demo")
	}
	
	join := newJoinAnimation()
	
	stages := []struct {
		name    string
		pct     float64
		current int
		total   int
		frames  int
	}{
		{"Starting", 0.1, 1, 8, 5},
		{"Mid-assembly", 0.5, 4, 8, 5},
		{"Almost done", 0.9, 7, 8, 5},
		{"Complete", 1.0, 8, 8, 5},
	}
	
	for _, stage := range stages {
		fmt.Printf("\n─── %s ───\n", stage.name)
		for i := 0; i < stage.frames; i++ {
			join.Tick()
		}
		fmt.Println(join.View(stage.pct, stage.current, stage.total))
	}
}

// TestVerifyAnimation demonstrates the GPG verification animation.
func TestVerifyAnimation(t *testing.T) {
	if testing.Short() {
		t.Skip("visual demo")
	}
	
	verify := newVerifyAnimation()
	
	fmt.Println("\n─── Verifying GPG Signature ───")
	
	// Show animation over several frames
	for i := 0; i < 20; i++ {
		verify.Tick()
	}
	
	fmt.Println(verify.View())
}
