package ui

import (
	"fmt"
	"testing"
	"time"
)

// TestDataLinkVisualDemo prints the data-link panel at three states for
// visual inspection.  Run: go test ./internal/ui -run VisualDemo -v
func TestDataLinkVisualDemo(t *testing.T) {
	if testing.Short() {
		t.Skip("visual demo")
	}
	d := newDataLink()
	mkChans := func(n int, gen func(i int) channelRow) []channelRow {
		out := make([]channelRow, n)
		for i := 0; i < n; i++ {
			out[i] = gen(i)
		}
		return out
	}

	cases := []struct {
		label    string
		channels []channelRow
		total    int64
		size     int64
		peak     float64
		elapsed  time.Duration
		status   string
		carrier  bool
	}{
		{
			label: "warming up",
			channels: mkChans(4, func(i int) channelRow {
				return channelRow{Index: i, Pct: 0.02, RawPct: 0.02, Speed: 0}
			}),
			total: 0, size: 100_000_000, peak: 0,
			elapsed: 800 * time.Millisecond, status: "handshake", carrier: false,
		},
		{
			label: "mid transfer (4 ch)",
			channels: []channelRow{
				{Index: 0, Pct: 0.72, RawPct: 0.72, Speed: 1_200_000, HasStarted: true},
				{Index: 1, Pct: 0.56, RawPct: 0.56, Speed: 980_000, HasStarted: true},
				{Index: 2, Pct: 0.88, RawPct: 0.88, Speed: 1_500_000, HasStarted: true},
				{Index: 3, Pct: 0.64, RawPct: 0.64, Speed: 1_100_000, HasStarted: true},
			},
			total: 73_800_000, size: 100_000_000,
			peak: 6_200_000, elapsed: 12 * time.Second,
			status: "downloading", carrier: true,
		},
		{
			label: "near completion (8 ch, mixed)",
			channels: []channelRow{
				{Index: 0, Pct: 1.0, RawPct: 1.0, Done: true},
				{Index: 1, Pct: 1.0, RawPct: 1.0, Done: true},
				{Index: 2, Pct: 0.95, RawPct: 0.95, Speed: 850_000, HasStarted: true},
				{Index: 3, Pct: 0.92, RawPct: 0.92, Speed: 720_000, HasStarted: true},
				{Index: 4, Pct: 1.0, RawPct: 1.0, Done: true},
				{Index: 5, Pct: 0.97, RawPct: 0.97, Speed: 920_000, HasStarted: true},
				{Index: 6, Pct: 1.0, RawPct: 1.0, Done: true},
				{Index: 7, Pct: 0.93, RawPct: 0.93, Speed: 680_000, HasStarted: true},
			},
			total: 96_000_000, size: 100_000_000,
			peak: 12_800_000, elapsed: 25 * time.Second,
			status: "downloading", carrier: true,
		},
	}

	for _, c := range cases {
		// Tick a few times so LEDs reach steady state for snapshot.
		partSpeeds := make([]float64, len(c.channels))
		var total float64
		for i, ch := range c.channels {
			partSpeeds[i] = ch.Speed
			total += ch.Speed
		}
		for i := 0; i < 6; i++ {
			d.Tick(total, c.peak, partSpeeds)
		}
		fmt.Println("─── " + c.label + " ───")
		fmt.Println(d.View(c.channels, c.total, c.size, c.peak, c.elapsed, c.status, c.carrier))
		fmt.Println()
	}
}
