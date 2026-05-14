package extractor

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSink captures every event the parser emits.
type fakeSink struct {
	mu       sync.Mutex
	meta     *Meta
	progress []DownloadProgress
	phases   []Phase
	logs     []string
}

func (f *fakeSink) OnMeta(m Meta) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.meta = &m
}
func (f *fakeSink) OnDownloadProgress(p DownloadProgress) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.progress = append(f.progress, p)
}
func (f *fakeSink) OnPhaseChange(ph Phase) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.phases = append(f.phases, ph)
}
func (f *fakeSink) OnLog(level, line string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logs = append(f.logs, level+":"+line)
}

// fakeYTDLPScript writes a deterministic stream of HGET| lines to stdout
// so we can exercise the parser without depending on the network or on a
// specific yt-dlp version.  We swap PATH temporarily so the subprocess
// dispatch picks up our shim binary instead of the real yt-dlp.
const fakeScript = `#!/bin/sh
echo "[vimeo] Extracting URL"
echo "[download] Destination: /tmp/Test.f137.mp4"
echo "HGET|  0.0%|0|1000000|0|10|NA|NA"
echo "HGET| 25.0%|250000|1000000|500000|6|NA|NA"
echo "HGET| 50.0%|500000|1000000|800000|3|NA|NA"
echo "HGET| 75.0%|750000|1000000|1200000|1|NA|NA"
echo "HGET|100.0%|1000000|1000000|1500000|0|NA|NA"
echo "[Merger] Merging formats into \"/tmp/Test.mp4\""
exit 0
`

func TestRun_StreamsProgressAndPhasesViaSink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shim script is POSIX-only")
	}

	// Build a temp PATH containing only our shim.  The real yt-dlp on
	// the developer's machine never gets invoked in this test.
	tmp := t.TempDir()
	shim := filepath.Join(tmp, "yt-dlp")
	if err := os.WriteFile(shim, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	t.Setenv("PATH", tmp)

	sink := &fakeSink{}
	out, err := Run(context.Background(), "https://example.com/x", "", Options{}, FormatSelection{}, sink)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if out != "/tmp/Test.mp4" {
		t.Errorf("output path: got %q want /tmp/Test.mp4", out)
	}

	if got := len(sink.progress); got != 5 {
		t.Errorf("expected 5 progress events, got %d", got)
	}
	if len(sink.progress) >= 5 {
		want := []float64{0.0, 25.0, 50.0, 75.0, 100.0}
		for i, w := range want {
			if sink.progress[i].Percent != w {
				t.Errorf("progress[%d].Percent = %v, want %v", i, sink.progress[i].Percent, w)
			}
		}
		mid := sink.progress[2]
		if mid.Downloaded != 500000 || mid.Total != 1000000 {
			t.Errorf("midpoint downloaded/total = %d/%d, want 500000/1000000", mid.Downloaded, mid.Total)
		}
		if mid.SpeedBPS != 800000 {
			t.Errorf("midpoint speed = %v, want 800000", mid.SpeedBPS)
		}
		if mid.ETA != 3*time.Second {
			t.Errorf("midpoint ETA = %v, want 3s", mid.ETA)
		}
	}

	// Phase ordering: Downloading first, then Muxing on the [Merger] line.
	if len(sink.phases) < 2 {
		t.Fatalf("expected at least 2 phase changes, got %d (%v)", len(sink.phases), sink.phases)
	}
	if sink.phases[0] != PhaseDownloading {
		t.Errorf("first phase = %v, want PhaseDownloading", sink.phases[0])
	}
	sawMuxing := false
	for _, p := range sink.phases {
		if p == PhaseMuxing {
			sawMuxing = true
		}
	}
	if !sawMuxing {
		t.Errorf("expected PhaseMuxing after [Merger] line, got phases=%v", sink.phases)
	}

	// Done phase fires from sink at the end of Run().
	if sink.phases[len(sink.phases)-1] != PhaseDone {
		t.Errorf("last phase = %v, want PhaseDone", sink.phases[len(sink.phases)-1])
	}
}

// TestRun_StderrLinesSurfaceAsWarnLogs proves stderr output flows
// through the log channel rather than getting silently dropped.
func TestRun_StderrLinesSurfaceAsWarnLogs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shim script is POSIX-only")
	}
	tmp := t.TempDir()
	shim := filepath.Join(tmp, "yt-dlp")
	stderrScript := `#!/bin/sh
echo "WARNING: this is on stderr" 1>&2
echo "HGET| 50.0%|500|1000|100|2|NA|NA"
exit 0
`
	if err := os.WriteFile(shim, []byte(stderrScript), 0o755); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	t.Setenv("PATH", tmp)

	sink := &fakeSink{}
	if _, err := Run(context.Background(), "https://x.com/y", "", Options{}, FormatSelection{}, sink); err != nil {
		t.Fatalf("Run: %v", err)
	}
	gotWarn := false
	for _, l := range sink.logs {
		if strings.HasPrefix(l, "warn:") && strings.Contains(l, "WARNING") {
			gotWarn = true
		}
	}
	if !gotWarn {
		t.Errorf("expected stderr to surface as warn log; got logs=%v", sink.logs)
	}
}

// confirms our shim mechanism actually replaces yt-dlp on PATH (sanity check)
func TestShimResolution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shim script is POSIX-only")
	}
	tmp := t.TempDir()
	shim := filepath.Join(tmp, "yt-dlp")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\necho shim-output\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmp)
	out, err := exec.Command("yt-dlp").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "shim-output" {
		t.Errorf("PATH override didn't pick up shim; got %q", out)
	}
}

// silence unused import warning when building without exec usage above
var _ = io.EOF
