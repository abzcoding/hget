package ui

import (
	"fmt"
	"testing"
	"time"
)

// TestDataLinkStates demonstrates LED patterns for each state.
func TestDataLinkStates(t *testing.T) {
	if testing.Short() {
		t.Skip("visual demo")
	}
	d := newDataLink()

	states := []struct {
		label    string
		status   string
		carrier  bool
		channels []channelRow
		total    int64
		size     int64
		peak     float64
		elapsed  time.Duration
	}{
		{
			label:   "HANDSHAKE (PWR/CD on, TX/RX blinking)",
			status:  "HANDSHAKE",
			carrier: false,
			channels: []channelRow{
				{Index: 1, Pct: 0.02, RawPct: 0.02, Speed: 0, HasStarted: true},
				{Index: 2, Pct: 0.02, RawPct: 0.02, Speed: 0, HasStarted: true},
				{Index: 3, Pct: 0.02, RawPct: 0.02, Speed: 0, HasStarted: true},
				{Index: 4, Pct: 0.02, RawPct: 0.02, Speed: 0, HasStarted: true},
			},
			total: 0, size: 100_000_000, peak: 0, elapsed: 1 * time.Second,
		},
		{
			label:   "DOWNLOADING (all LEDs active)",
			status:  "DOWNLOADING",
			carrier: true,
			channels: []channelRow{
				{Index: 1, Pct: 0.72, RawPct: 0.72, Speed: 1_100_000, HasStarted: true},
				{Index: 2, Pct: 0.56, RawPct: 0.56, Speed: 957_000, HasStarted: true},
				{Index: 3, Pct: 0.88, RawPct: 0.88, Speed: 1_400_000, HasStarted: true},
				{Index: 4, Pct: 0.64, RawPct: 0.64, Speed: 1_000_000, HasStarted: true},
			},
			total: 73_800_000, size: 100_000_000, peak: 5_900_000, elapsed: 12 * time.Second,
		},
		{
			label:   "STOPPING (PWR/CD on, OH blinking amber)",
			status:  "STOPPING",
			carrier: false,
			channels: []channelRow{
				{Index: 1, Pct: 0.72, RawPct: 0.72, Speed: 0, HasStarted: true},
				{Index: 2, Pct: 0.56, RawPct: 0.56, Speed: 0, HasStarted: true},
				{Index: 3, Pct: 0.88, RawPct: 0.88, Speed: 0, HasStarted: true},
				{Index: 4, Pct: 0.64, RawPct: 0.64, Speed: 0, HasStarted: true},
			},
			total: 73_800_000, size: 100_000_000, peak: 5_900_000, elapsed: 15 * time.Second,
		},
		{
			label:   "COMPLETE (all LEDs solid, channels 100%)",
			status:  "COMPLETE",
			carrier: true,
			channels: []channelRow{
				{Index: 1, Pct: 1.0, RawPct: 1.0, Speed: 0, Done: true, HasStarted: true},
				{Index: 2, Pct: 1.0, RawPct: 1.0, Speed: 0, Done: true, HasStarted: true},
				{Index: 3, Pct: 1.0, RawPct: 1.0, Speed: 0, Done: true, HasStarted: true},
				{Index: 4, Pct: 1.0, RawPct: 1.0, Speed: 0, Done: true, HasStarted: true},
			},
			total: 100_000_000, size: 100_000_000, peak: 12_200_000, elapsed: 20 * time.Second,
		},
		{
			label:   "ERROR (PWR on, OH solid magenta)",
			status:  "ERROR",
			carrier: false,
			channels: []channelRow{
				{Index: 1, Pct: 0.45, RawPct: 0.45, Speed: 0, HasStarted: true},
				{Index: 2, Pct: 0.32, RawPct: 0.32, Speed: 0, HasStarted: true},
				{Index: 3, Pct: 0.51, RawPct: 0.51, Speed: 0, HasStarted: true},
				{Index: 4, Pct: 0.38, RawPct: 0.38, Speed: 0, HasStarted: true},
			},
			total: 45_000_000, size: 100_000_000, peak: 5_900_000, elapsed: 8 * time.Second,
		},
	}

	for _, s := range states {
		// Tick a few times to let LEDs settle
		partSpeeds := make([]float64, len(s.channels))
		var totalSpeed float64
		for i, ch := range s.channels {
			partSpeeds[i] = ch.Speed
			totalSpeed += ch.Speed
		}
		for i := 0; i < 5; i++ {
			d.Tick(totalSpeed, s.peak, partSpeeds)
		}
		
		fmt.Println("─── " + s.label + " ───")
		fmt.Println(d.View(s.channels, s.total, s.size, s.peak, s.elapsed, s.status, s.carrier))
		fmt.Println()
	}
}
