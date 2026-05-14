// Package extractor wraps external media extractors (currently yt-dlp)
// and the post-processing toolchain (ffmpeg muxing).  The package emits
// structured Tea messages so the UI layer can render bespoke animations
// (VCR for download, mixer for muxing) without parsing subprocess output
// itself.
package extractor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrNotInstalled is returned when yt-dlp is missing from $PATH.
var ErrNotInstalled = errors.New("yt-dlp is not installed or not on $PATH")

// Options carries optional knobs threaded into every yt-dlp invocation
// (probe + download).  The zero value is valid and means "no extras".
//
// We deliberately group these into a struct so adding the next auth /
// proxy / format knob doesn't ripple through every Probe/Run/Pipeline
// signature.
type Options struct {
	// CookiesFile is a path to a Netscape-format cookies.txt that
	// yt-dlp will use for authentication.  Equivalent to passing
	// `--cookies <path>` to yt-dlp.  Empty means no cookie file.
	CookiesFile string

	// CookiesFromBrowser is a yt-dlp browser spec
	// `BROWSER[+KEYRING][:PROFILE][::CONTAINER]` — e.g. "firefox",
	// "chrome:Default", "firefox:my-profile".  Equivalent to passing
	// `--cookies-from-browser <spec>`.  Empty means no browser cookie
	// extraction.
	CookiesFromBrowser string
}

// authArgs returns the yt-dlp CLI fragments the Options struct contributes.
// Returns an empty slice when no auth knobs are set.  Validation of the
// values themselves is left to yt-dlp (it produces good error messages).
func (o Options) authArgs() []string {
	var args []string
	if o.CookiesFile != "" {
		args = append(args, "--cookies", o.CookiesFile)
	}
	if o.CookiesFromBrowser != "" {
		args = append(args, "--cookies-from-browser", o.CookiesFromBrowser)
	}
	return args
}

// MetaSink receives streaming events as the yt-dlp child process emits
// them.  Implementations are expected to be cheap (channel send / Tea
// Program.Send) — the parser does not buffer.
type MetaSink interface {
	OnMeta(Meta)
	OnDownloadProgress(DownloadProgress)
	OnPhaseChange(Phase)
	OnLog(level, line string)
}

// Phase is the high-level stage of the extractor pipeline.
type Phase int

const (
	PhaseProbing Phase = iota // metadata fetch (yt-dlp -J)
	PhaseDownloading
	PhaseMuxing  // yt-dlp post-processing (Merger / Audio extraction)
	PhaseDone
	PhaseError
)

// Meta carries the resolved video metadata.
type Meta struct {
	Title       string
	Uploader    string
	Duration    time.Duration
	VideoFormat string // chosen video stream id ("248")
	AudioFormat string // chosen audio stream id ("251") — empty when single-stream
	Container   string // final container ("mp4", "webm", "mkv")
	OutputFile  string // final output file path (resolved post-merge)
	Resolution  string // e.g. "1920x1080"
	FPS         float64
	VCodec      string
	ACodec      string
	Filesize    int64 // best-effort estimate from -J ("filesize" or "filesize_approx")
	IsLive      bool  // true for live streams — skips the format selector

	// Formats is the full, cleaned format table extracted from yt-dlp's
	// -J output.  Empty when -J didn't populate the formats[] array
	// (some single-stream sources).  Renderers should treat an empty
	// list as "fall back to default spec".
	Formats []Format
}

// FormatSelection is the user's choice (or programmatic default) for
// which streams yt-dlp should download and how to package them.
//
// The zero value is valid and means "yt-dlp's default best-video+audio
// pick into mp4" — keeping callers that don't care about selection
// (non-TTY, --format flag absent) one line shorter.
type FormatSelection struct {
	// Spec is forwarded as the value of `-f`.  Examples:
	//
	//   "bv*+ba/b"     // default — best video + best audio, single fallback
	//   "248+251"      // explicit pair (separate streams, will mux)
	//   "22"           // single progressive format (no mux needed)
	//   "bv[height<=720]+ba"
	Spec string

	// Container is forwarded as the value of `--merge-output-format`.
	// Ignored when Spec resolves to a single progressive format
	// (yt-dlp skips the merger in that path).
	Container string
}

// Args renders the selection as yt-dlp CLI fragments, applying defaults
// for empty fields.  Always returns a non-empty slice.
func (s FormatSelection) Args() []string {
	spec := s.Spec
	if spec == "" {
		spec = "bv*+ba/b"
	}
	cont := s.Container
	if cont == "" {
		cont = "mp4"
	}
	return []string{"-f", spec, "--merge-output-format", cont}
}

// SelectorFunc is invoked between Probe and Run.  It receives the
// resolved metadata (with Formats populated) and returns the user's
// choice.  A nil SelectorFunc means "use FormatSelection{}".  Returning
// a non-nil error aborts the pipeline before yt-dlp is spawned.
type SelectorFunc func(ctx context.Context, meta Meta) (FormatSelection, error)

// DownloadProgress is the parsed state of one yt-dlp [download] line.
type DownloadProgress struct {
	Percent      float64
	Downloaded   int64
	Total        int64
	SpeedBPS     float64
	ETA          time.Duration
	Fragment     int    // current fragment index (HLS / DASH); -1 if N/A
	FragmentN    int    // total fragments; -1 if N/A
	StreamLabel  string // "video" / "audio" / "" — derived from format id transitions
	RawSpeedText string
}

// Probe runs `yt-dlp -J <url>` and returns metadata.  Honours ctx.
// Auth knobs in opts are forwarded so probes against gated sites
// (YouTube bot challenge, age-gates, members-only videos) work too.
func Probe(ctx context.Context, url string, opts Options) (Meta, error) {
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		return Meta{}, ErrNotInstalled
	}
	args := []string{"-J", "--no-warnings", "--no-playlist"}
	args = append(args, opts.authArgs()...)
	args = append(args, url)
	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return Meta{}, fmt.Errorf("yt-dlp probe failed: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return Meta{}, fmt.Errorf("yt-dlp probe failed: %w", err)
	}
	return parseMetaJSON(out)
}

// progressTemplate uses yt-dlp's --progress-template to print a stable,
// pipe-delimited progress line we can parse without regex tap-dancing.
//
// The leading `download:` is required — without an explicit TYPE prefix
// yt-dlp silently discards the template instead of using it for download
// progress, and our VCR panel ends up with zero updates.
//
// Fields, in order:
//
//	PCT|DOWNLOADED|TOTAL|SPEED|ETA|FRAGINDEX|FRAGTOTAL
//
// We re-parse the numeric values in Go because yt-dlp's pre-formatted
// `_*_str` values are locale-sensitive and noisy.
const progressTemplate = "download:HGET|%(progress._percent_str)s|%(progress.downloaded_bytes)s|%(progress.total_bytes,progress.total_bytes_estimate)s|%(progress.speed)s|%(progress.eta)s|%(progress.fragment_index)s|%(progress.fragment_count)s"

// Run streams a download via yt-dlp.  Events flow through sink.  The
// child process is killed when ctx is cancelled (CommandContext does the
// SIGKILL).  Returns the chosen output path on success.
//
// `sel` controls which format spec / container yt-dlp receives.  The
// zero value falls back to "best video + audio merged to mp4" — the
// pre-selector behaviour, preserved for non-TTY callers.
func Run(ctx context.Context, url, outDir string, opts Options, sel FormatSelection, sink MetaSink) (string, error) {
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		return "", ErrNotInstalled
	}

	// `--print` is intentionally omitted: in yt-dlp it implies `--quiet`
	// which silences both download progress and the [Merger] line we use
	// to detect the muxing phase.  Instead we parse `[download]
	// Destination:` and `[Merger] Merging formats into "..."` ourselves.
	args := []string{
		"--no-warnings",
		"--no-playlist",
		"--newline", // emit one progress line per update
		"--progress-template", progressTemplate,
	}
	args = append(args, sel.Args()...)
	args = append(args, "-o", "%(title)s.%(ext)s")
	args = append(args, opts.authArgs()...)
	args = append(args, url)
	if outDir != "" {
		args = append([]string{"-P", outDir}, args...)
	}

	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}

	if err := cmd.Start(); err != nil {
		return "", err
	}

	var (
		outPath  string
		mu       sync.Mutex
		setPath  = func(p string) { mu.Lock(); outPath = p; mu.Unlock() }
		wg       sync.WaitGroup
		curPhase = PhaseDownloading
	)
	sink.OnPhaseChange(PhaseDownloading)

	scan := func(r io.Reader, isErr bool) {
		defer wg.Done()
		s := bufio.NewScanner(r)
		s.Buffer(make([]byte, 0, 1<<16), 1<<20)
		for s.Scan() {
			line := s.Text()
			if strings.HasPrefix(line, "HGET|") {
				if dp, ok := parseProgressLine(line); ok {
					sink.OnDownloadProgress(dp)
				}
				continue
			}
			// Resolved output path discovery — non-mux downloads
			// surface as `[download] Destination: <path>`, merged
			// downloads finish with `[Merger] Merging formats into
			// "<path>"`.  Both shapes get extracted here so callers
			// always see the final filename.
			if p, ok := extractDestination(line); ok {
				setPath(p)
			}
			// Phase transitions detected from yt-dlp's own log lines.
			switch {
			case strings.HasPrefix(line, "[Merger]"),
				strings.HasPrefix(line, "[ExtractAudio]"),
				strings.HasPrefix(line, "[ffmpeg]"),
				strings.HasPrefix(line, "[VideoConvertor]"):
				if curPhase != PhaseMuxing {
					curPhase = PhaseMuxing
					sink.OnPhaseChange(PhaseMuxing)
				}
				sink.OnLog("info", line)
			default:
				lvl := "info"
				if isErr {
					lvl = "warn"
				}
				if line != "" {
					sink.OnLog(lvl, line)
				}
			}
		}
	}

	wg.Add(2)
	go scan(stdout, false)
	go scan(stderr, true)
	wg.Wait()

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return outPath, ctx.Err()
		}
		return outPath, fmt.Errorf("yt-dlp exited: %w", err)
	}
	sink.OnPhaseChange(PhaseDone)
	return outPath, nil
}

// ── parsers ────────────────────────────────────────────────────────────

var (
	floatRE = regexp.MustCompile(`-?\d+(\.\d+)?`)
	// `[download] Destination: <path>` — fired before the bytes start
	// flowing.  Robust against absolute/relative paths and spaces.
	destRE = regexp.MustCompile(`^\[download\] Destination:\s+(.+)$`)
	// `[Merger] Merging formats into "<path>"` — only present when
	// yt-dlp invokes ffmpeg to combine separate v+a streams.  We prefer
	// this path when both lines fire, since it reflects the merged file.
	mergeRE = regexp.MustCompile(`^\[Merger\] Merging formats into "([^"]+)"`)
)

// extractDestination pulls the resolved output path out of a yt-dlp log
// line.  Returns ("", false) when the line is unrelated.
func extractDestination(line string) (string, bool) {
	if m := mergeRE.FindStringSubmatch(line); len(m) >= 2 {
		return strings.TrimSpace(m[1]), true
	}
	if m := destRE.FindStringSubmatch(line); len(m) >= 2 {
		return strings.TrimSpace(m[1]), true
	}
	return "", false
}

func parseProgressLine(line string) (DownloadProgress, bool) {
	// HGET|PCT|DOWNLOADED|TOTAL|SPEED|ETA|FRAGINDEX|FRAGTOTAL
	parts := strings.Split(line, "|")
	if len(parts) < 8 {
		return DownloadProgress{}, false
	}
	dp := DownloadProgress{Fragment: -1, FragmentN: -1}

	if m := floatRE.FindString(parts[1]); m != "" {
		if v, err := strconv.ParseFloat(m, 64); err == nil {
			dp.Percent = v
		}
	}
	dp.Downloaded = parseInt64(parts[2])
	dp.Total = parseInt64(parts[3])
	if v, err := strconv.ParseFloat(parts[4], 64); err == nil {
		dp.SpeedBPS = v
	}
	if v, err := strconv.Atoi(parts[5]); err == nil {
		dp.ETA = time.Duration(v) * time.Second
	}
	if v, err := strconv.Atoi(parts[6]); err == nil {
		dp.Fragment = v
	}
	if v, err := strconv.Atoi(parts[7]); err == nil {
		dp.FragmentN = v
	}
	return dp, true
}

func parseInt64(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "NA" || s == "None" {
		return 0
	}
	if v, err := strconv.ParseInt(s, 10, 64); err == nil {
		return v
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(v)
	}
	return 0
}
