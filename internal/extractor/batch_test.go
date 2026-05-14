package extractor

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestWrapBatchSelector_FormatAllCachesFirstPick(t *testing.T) {
	var calls int32
	sel := func(_ context.Context, _ Meta) (FormatSelection, error) {
		atomic.AddInt32(&calls, 1)
		return FormatSelection{
			Spec:      "248+251",
			Container: "mkv",
			Pref:      FormatPreference{HeightCeiling: 1080, VCodec: "vp9", ABRCeiling: 160},
		}, nil
	}
	wrapped := wrapBatchSelector(BatchFormatAll, sel)

	// First call must invoke the underlying selector and return the
	// verbatim Spec (tape #1 picked from its own format list).
	a, _ := wrapped(context.Background(), Meta{Title: "one"})
	if a.Spec != "248+251" || a.Container != "mkv" {
		t.Errorf("first call returned %+v", a)
	}
	// Subsequent calls must reuse the cached Pref but **drop** the
	// exact Spec so the adaptive filter expression drives subsequent
	// tapes — that's the whole point of FormatAll's robustness.
	for i := 0; i < 5; i++ {
		b, _ := wrapped(context.Background(), Meta{Title: "n"})
		if b.Spec != "" {
			t.Errorf("cached call %d should have empty Spec, got %q", i, b.Spec)
		}
		if b.Container != a.Container {
			t.Errorf("cached call %d container diverged: %q vs %q", i, b.Container, a.Container)
		}
		if b.Pref != a.Pref {
			t.Errorf("cached call %d Pref diverged: %+v vs %+v", i, b.Pref, a.Pref)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("FormatAll fired selector %d times, want 1", got)
	}
}

func TestWrapBatchSelector_FormatEachFiresEveryCall(t *testing.T) {
	var calls int32
	sel := func(_ context.Context, _ Meta) (FormatSelection, error) {
		atomic.AddInt32(&calls, 1)
		return FormatSelection{Spec: "best"}, nil
	}
	wrapped := wrapBatchSelector(BatchFormatEach, sel)
	for i := 0; i < 4; i++ {
		_, _ = wrapped(context.Background(), Meta{})
	}
	if got := atomic.LoadInt32(&calls); got != 4 {
		t.Errorf("FormatEach fired selector %d times, want 4", got)
	}
}

func TestWrapBatchSelector_PresetIgnoresSelector(t *testing.T) {
	var calls int32
	sel := func(_ context.Context, _ Meta) (FormatSelection, error) {
		atomic.AddInt32(&calls, 1)
		return FormatSelection{Spec: "never"}, nil
	}
	wrapped := wrapBatchSelector(BatchFormatPreset, sel)
	got, _ := wrapped(context.Background(), Meta{})
	if got != (FormatSelection{}) {
		t.Errorf("Preset must return zero FormatSelection, got %+v", got)
	}
	if calls != 0 {
		t.Errorf("Preset fired selector %d times, want 0", calls)
	}
}

func TestWrapBatchSelector_NilStaysNil(t *testing.T) {
	if w := wrapBatchSelector(BatchFormatAll, nil); w != nil {
		t.Errorf("nil selector must stay nil regardless of policy")
	}
}

func TestSummariseShelf_TalliesByStatus(t *testing.T) {
	// Import-via-alias trick: the helper accepts ui.CassetteItem but we
	// don't want to bring the ui package into a pure-logic test.  This
	// test exists at the boundary so use the actual import.
	t.Skip("SummariseShelf signature uses ui.CassetteItem — covered by an integration test in the ui package")
}
