package ui

import (
	"strings"
	"testing"
	"time"
)

func TestCassetteShelf_SeedsPlaceholders(t *testing.T) {
	s := NewCassetteShelf([]string{
		"https://youtu.be/aaa",
		"https://youtu.be/bbb",
		"https://youtu.be/ccc",
	})
	if s.Len() != 3 {
		t.Fatalf("Len=%d want 3", s.Len())
	}
	out := stripANSI(s.View(100))
	for _, want := range []string{"tape 01", "tape 02", "tape 03", "⏵ 00 / 03"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in seed view:\n%s", want, out)
		}
	}
}

func TestCassetteShelf_MetaPopulatesSpine(t *testing.T) {
	s := NewCassetteShelf([]string{"u1", "u2"})
	s.SetMeta(0, "Linux Kernel 6.8", "ThePrimeagen", "1920x1080", 4*time.Minute+32*time.Second)
	out := stripANSI(s.View(120))
	mustContain(t, out, "Linux", "title not rendered on lush tier")
	mustContain(t, out, "1080p", "compact resolution missing")
	mustContain(t, out, "04:32", "runtime stamp missing")
	mustContain(t, out, "THE", "channel chip abbreviation missing")
}

func TestCassetteShelf_StatusTallyCounter(t *testing.T) {
	s := NewCassetteShelf([]string{"a", "b", "c", "d"})
	s.SetStatus(0, CassetteDone, "")
	s.SetStatus(1, CassetteFailed, "boom")
	s.SetStatus(2, CassetteSkipped, "")
	s.SetActive(3)
	out := stripANSI(s.View(120))
	mustContain(t, out, "✓ 1 done", "done tally missing")
	mustContain(t, out, "✗ 1 failed", "failed tally missing")
	mustContain(t, out, "− 1 skipped", "skipped tally missing")
	mustContain(t, out, "⏵ 04 / 04", "active counter missing")
}

func TestCassetteShelf_ActiveLifts(t *testing.T) {
	s := NewCassetteShelf([]string{"a", "b"})
	s.SetActive(1)
	// Run enough ticks for the spring to saturate.
	for i := 0; i < 30; i++ {
		s.Tick()
	}
	if s.lift[1] < 0.9 {
		t.Errorf("active cassette didn't lift; lift=%.2f", s.lift[1])
	}
	if s.lift[0] > 0.1 {
		t.Errorf("inactive cassette lifted; lift=%.2f", s.lift[0])
	}
}

func TestCassetteShelf_AdaptiveTiers(t *testing.T) {
	urls := []string{"a", "b", "c", "d", "e", "f"}
	s := NewCassetteShelf(urls)
	for i := range urls {
		s.SetMeta(i, "Some Long Title For Tape", "Channel", "1080p", 60*time.Second)
	}

	// Wide terminal — should hit lush tier (channel chip visible).
	wide := stripANSI(s.View(200))
	if !strings.Contains(wide, "CHA") {
		t.Errorf("wide terminal should show channel chip:\n%s", wide)
	}

	// Mid-width terminal — the planner may keep lush tier but with
	// fewer cassettes visible.  Either way the active should still be
	// rendered and the counter strip stays intact.
	mid := stripANSI(s.View(50))
	mustContain(t, mid, "⏵", "counter strip dropped at mid width")

	// Narrow terminal forces a lower tier.  At width 30 with 6 items,
	// the planner reduces to ~2 cassettes at compact tier — channel
	// chip is dropped, status pill remains.  Whether the № index
	// marker fits depends on the runtime label width (compactFooter
	// degrades to "MM:SS" when both don't fit) so we just confirm
	// the status pill survived.
	narrow := stripANSI(s.View(30))
	if strings.Contains(narrow, "CHA") {
		t.Errorf("narrow terminal still rendering channel chip:\n%s", narrow)
	}
	mustContain(t, narrow, "[⏸]", "compact tier missing status pill")

	// Very narrow terminal — degrades cleanly without panicking.
	if got := s.View(18); got == "" {
		t.Skip("tiny terminal returned empty view (below minimum width)")
	}
}

func TestCassetteShelf_WindowsAroundActive(t *testing.T) {
	urls := make([]string, 20)
	for i := range urls {
		urls[i] = "u"
	}
	s := NewCassetteShelf(urls)
	s.SetActive(10)
	out := stripANSI(s.View(80))
	// The active index numeral must be present.
	if !strings.Contains(out, "11") {
		t.Errorf("active index 11 not visible:\n%s", out)
	}
	// Far-away indices (01, 20) should be collapsed into overflow stubs.
	if strings.Contains(out, " 01 ") {
		t.Errorf("index 01 visible despite being far from active:\n%s", out)
	}
}

func TestSplitTitle_WrapsAtWordBoundary(t *testing.T) {
	a, b := splitTitle("Linux Kernel 6.8 Released", 10)
	if a != "Linux" || !strings.HasPrefix(b, "Kernel") {
		t.Errorf("splitTitle wrap: a=%q b=%q", a, b)
	}
}
