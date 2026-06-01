package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
)

// ── Extractor-mode Tea messages ──────────────────────────────────────────────
//
// Kept in this file (rather than ui.go) because they're specific to the
// extractor pipeline and must not collide with the existing TUI model's
// download / verify message vocabulary.

// ExtractorMetaMsg seeds the VCR header with metadata from yt-dlp -J.
type ExtractorMetaMsg struct {
	Title      string
	Channel    string
	Duration   time.Duration
	Resolution string
	FPS        float64
	VCodec     string
	ACodec     string
	Container  string
	HasAudio   bool
	OutputFile string // hint — actual path resolved post-merge
}

// ExtractorProgressMsg pushes one progress update from yt-dlp.
type ExtractorProgressMsg struct {
	Percent    float64 // 0..100
	Downloaded int64
	Total      int64
	SpeedBPS   float64
	ETA        time.Duration
	Fragment   int
	FragmentN  int
}

// ExtractorPhaseMsg switches the active panel: VCR vs Mixer vs done.
type ExtractorPhaseMsg struct {
	Phase string // "downloading" | "muxing" | "done" | "error"
}

// ExtractorOutputMsg surfaces yt-dlp's resolved output path post-merge.
type ExtractorOutputMsg struct{ Path string }

// ExtractorLogMsg adds a line to the on-screen log strip.
type ExtractorLogMsg struct {
	Level string // "info" | "warn" | "error"
	Text  string
}

// ExtractorErrorMsg signals a fatal error in the extractor pipeline.
type ExtractorErrorMsg struct{ Err error }

// ExtractorDoneMsg signals success — auto-quit countdown begins.
type ExtractorDoneMsg struct{}

// ExtractorFormatsMsg seeds the VCR's browsing-mode selector with the
// format table parsed from yt-dlp's -J output.  Sent immediately after
// the metadata message during the probe → select handshake.
//
// An empty Video slice means there are no separately-selectable video
// streams; the model then skips the browser entirely and falls through
// to yt-dlp's default format spec.
type ExtractorFormatsMsg struct {
	Video      []ExtractorFormat
	Audio      []ExtractorFormat
	Containers []string
	IsLive     bool // live streams always skip the selector
}

// ExtractorSelectionMsg is internal — the model emits it when the user
// commits a format choice in browsing mode.  The runner forwards the
// payload to the worker goroutine via a buffered channel.
//
// In addition to the exact format spec, the message carries adaptive
// descriptors (height ceiling, codec hint, audio bitrate ceiling).
// The batch pipeline uses those to build a yt-dlp filter expression
// that survives videos that lack the originally-picked format IDs.
type ExtractorSelectionMsg struct {
	Spec      string
	Container string

	HeightCeiling int
	FPSFloor      int
	VCodec        string
	ABRCeiling    int
	Progressive   bool
}

// ExtractorShelfSeedMsg installs the cassette shelf at batch start.
// One CassetteItem per URL.  Sent before any per-tape activity so the
// shelf is visible from the first frame.
type ExtractorShelfSeedMsg struct {
	URLs []string
}

// ExtractorShelfMetaMsg fills in the metadata for one cassette as
// probes resolve.  Fired by the eager probe pool so spine labels
// populate ahead of the deck reaching that tape.
type ExtractorShelfMetaMsg struct {
	Index      int
	Title      string
	Channel    string
	Resolution string
	Duration   time.Duration
}

// ExtractorShelfStatusMsg updates one cassette's lifecycle state.
type ExtractorShelfStatusMsg struct {
	Index  int
	Status CassetteStatus
	Err    string
}

// ExtractorShelfActiveMsg marks one cassette as the currently-playing
// tape.  Pass Index=-1 between items or at end-of-batch.
type ExtractorShelfActiveMsg struct {
	Index int
}

// ExtractorResetDeckMsg clears the deck (progress, output path, logs)
// between batch items so the next tape starts fresh.  The VCR returns
// to standby until the next ExtractorMetaMsg arrives.
type ExtractorResetDeckMsg struct{}

// extractorTickMsg drives animations.
type extractorTickMsg time.Time

// ── Model ────────────────────────────────────────────────────────────────────

type extractorModel struct {
	url     string
	width   int
	height  int
	spinner spinner.Model

	vcr   VCRAnimation
	mixer MixerAnimation
	phase string // "downloading" | "muxing" | "done" | "error"

	meta       ExtractorMetaMsg
	logs       []logEntry
	maxLogs    int
	outputPath string

	stopping bool
	hasError bool
	errMsg   string
	done     bool

	// Browsing state — only meaningful while phase=="selecting".
	browsing      bool
	hasSelection  bool // false once we've committed; gates key bindings
	onQuit        func()
	onSelection   func(ExtractorSelectionMsg) // wired by RunExtractorTUI
	startT        time.Time

	// Batch-mode cassette shelf.  Nil for single-URL extractor runs;
	// non-nil once ExtractorShelfSeedMsg arrives, rendered above the
	// VCR for the lifetime of the program.
	shelf *CassetteShelf
}

// NewExtractorModel constructs the extractor-mode TUI model.
func NewExtractorModel(url string, onQuit func()) extractorModel {
	s := spinner.New()
	s.Spinner = signalPulse
	s.Style = lipgloss.NewStyle().Foreground(Theme.Magenta)
	return extractorModel{
		url:     url,
		width:   80,
		spinner: s,
		vcr:     NewVCR(),
		mixer:   NewMixer(),
		phase:   "probing",
		maxLogs: 5,
		onQuit:  onQuit,
		startT:  time.Now(),
	}
}

// SetSelectionCallback wires the model's "user pressed REC" handler to
// the runner's selection channel.  Called by RunExtractorTUI; tests can
// supply their own to assert against a captured payload.
func (m *extractorModel) SetSelectionCallback(f func(ExtractorSelectionMsg)) {
	m.onSelection = f
}

// ExtractorURLMsg updates the "source" line shown above the shelf when
// the active tape changes mid-batch.  In single-URL mode the URL is
// fixed at construction time and this message is never sent.
type ExtractorURLMsg struct{ URL string }

func (m extractorModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, extractorTickCmd())
}

func extractorTickCmd() tea.Cmd {
	return tea.Tick(16*time.Millisecond, func(t time.Time) tea.Msg { return extractorTickMsg(t) })
}

func (m extractorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		// Browse-mode navigation is only live while we're armed and
		// waiting for the user.  Once REC is hit (hasSelection=true) we
		// fall through to the global quit handler so the deck behaves
		// like a normal recording session again.
		if m.browsing && !m.hasSelection {
			switch msg.String() {
			case "up", "k":
				m.vcr.CycleVideo(-1)
				return m, nil
			case "down", "j":
				m.vcr.CycleVideo(1)
				return m, nil
			case "left", "h":
				m.vcr.CycleAudio(-1)
				return m, nil
			case "right", "l":
				m.vcr.CycleAudio(1)
				return m, nil
			case "tab", "f":
				m.vcr.CycleContainer(1)
				return m, nil
			case "shift+tab":
				m.vcr.CycleContainer(-1)
				return m, nil
			case "enter", "r", "R":
				sel := m.vcr.Selection()
				m.hasSelection = true
				m.browsing = false
				m.phase = "downloading"
				m.vcr.SetMode(VCRRecording)
				if m.onSelection != nil {
					m.onSelection(sel)
				}
				return m, nil
			}
		}
		switch msg.String() {
		case "q", "Q", "ctrl+c":
			if m.stopping {
				return m, tea.Quit
			}
			m.stopping = true
			if m.onQuit != nil {
				m.onQuit()
			}
			return m, m.spinner.Tick
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case extractorTickMsg:
		m.vcr.Tick()
		m.mixer.Tick()
		if m.shelf != nil {
			m.shelf.Tick()
		}
		return m, extractorTickCmd()

	case ExtractorShelfSeedMsg:
		m.shelf = NewCassetteShelf(msg.URLs)
		return m, nil

	case ExtractorShelfMetaMsg:
		if m.shelf != nil {
			m.shelf.SetMeta(msg.Index, msg.Title, msg.Channel, msg.Resolution, msg.Duration)
		}
		return m, nil

	case ExtractorShelfStatusMsg:
		if m.shelf != nil {
			m.shelf.SetStatus(msg.Index, msg.Status, msg.Err)
		}
		return m, nil

	case ExtractorShelfActiveMsg:
		if m.shelf != nil {
			m.shelf.SetActive(msg.Index)
		}
		return m, nil

	case ExtractorURLMsg:
		m.url = msg.URL
		return m, nil

	case ExtractorResetDeckMsg:
		// Reset VCR + mixer + logs so the next tape starts clean.  We
		// don't touch the shelf — it tracks the whole batch.
		m.vcr = NewVCR()
		m.mixer = NewMixer()
		m.outputPath = ""
		m.meta = ExtractorMetaMsg{}
		m.browsing = false
		m.hasSelection = false
		m.phase = "probing"
		return m, nil

	case ExtractorMetaMsg:
		m.meta = msg
		m.vcr.SetMeta(VCRMeta{
			Title:      msg.Title,
			Channel:    msg.Channel,
			Duration:   msg.Duration,
			Resolution: msg.Resolution,
			FPS:        msg.FPS,
			VCodec:     msg.VCodec,
			ACodec:     msg.ACodec,
			Container:  msg.Container,
			HasAudio:   msg.HasAudio,
		})
		m.mixer.SetMeta(MixerMeta{
			VideoCodec: msg.VCodec,
			AudioCodec: msg.ACodec,
			Container:  msg.Container,
			OutputFile: msg.OutputFile,
		})
		return m, nil

	case ExtractorFormatsMsg:
		// Live streams and selector-less sources skip browsing entirely.
		if msg.IsLive || (len(msg.Video) == 0 && len(msg.Audio) == 0) {
			return m, nil
		}
		m.vcr.SetFormats(msg.Video, msg.Audio, msg.Containers)
		m.browsing = true
		m.hasSelection = false
		m.phase = "selecting"
		m.vcr.SetMode(VCRBrowsing)
		return m, nil

	case ExtractorPhaseMsg:
		// Suppress an early "downloading" message while we're still
		// browsing — the user committing REC owns that transition.
		if m.browsing && !m.hasSelection && msg.Phase == "downloading" {
			return m, nil
		}
		m.phase = msg.Phase
		switch msg.Phase {
		case "downloading":
			m.vcr.SetMode(VCRRecording)
			m.mixer.SetMode(MixerIdle)
		case "muxing":
			m.vcr.SetMode(VCREjecting)
			m.mixer.SetMode(MixerMixing)
		case "done":
			m.vcr.SetMode(VCREjecting)
			m.mixer.SetMode(MixerDone)
			m.done = true
			return m, tea.Tick(2500*time.Millisecond, func(t time.Time) tea.Msg { return ExtractorDoneMsg{} })
		case "error":
			m.vcr.SetMode(VCRError)
			m.mixer.SetMode(MixerError)
		}
		return m, nil

	case ExtractorProgressMsg:
		m.vcr.Update(msg.Percent, msg.Downloaded, msg.Total, msg.SpeedBPS, msg.ETA, msg.Fragment, msg.FragmentN)
		return m, nil

	case ExtractorOutputMsg:
		m.outputPath = msg.Path
		mm := m.mixer
		mm.meta.OutputFile = msg.Path
		m.mixer = mm
		return m, nil

	case ExtractorLogMsg:
		m.logs = append(m.logs, logEntry{level: msg.Level, text: msg.Text})
		if len(m.logs) > m.maxLogs {
			m.logs = m.logs[len(m.logs)-m.maxLogs:]
		}
		return m, nil

	case ExtractorErrorMsg:
		m.hasError = true
		m.phase = "error"
		if msg.Err != nil {
			m.errMsg = msg.Err.Error()
		} else {
			m.errMsg = "unknown error"
		}
		m.vcr.SetMode(VCRError)
		m.mixer.SetMode(MixerError)
		return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg { return ExtractorDoneMsg{} })

	case ExtractorDoneMsg:
		return m, tea.Quit
	}
	return m, nil
}

// View renders the extractor-mode screen.
func (m extractorModel) View() string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}

	sep := styleSep.Render(strings.Repeat("┄", sepWidth(w)))
	accentRule := lipgloss.NewStyle().Foreground(colorMagenta).Render(strings.Repeat("═", sepWidth(w)))

	// Banner — repurposed wordmark with a "VCR/MIX" strap.
	b.WriteString(styleBanner.Render(banner))
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Foreground(colorSteel).Render(
		"  ░▒▓ extractor link · yt-dlp pipeline · vcr → mixer ▓▒░"))
	b.WriteString("\n")
	b.WriteString(accentRule)
	b.WriteString("\n\n")

	// URL line.
	b.WriteString("  " + styleLabel.Render("source") +
		styleAccentValue.Render(truncate(m.url, w-12)) + "\n")
	if m.outputPath != "" {
		b.WriteString("  " + styleLabel.Render("output") +
			styleValue.Render(truncate(m.outputPath, w-12)) + "\n")
	} else if m.meta.OutputFile != "" {
		b.WriteString("  " + styleLabel.Render("output") +
			styleValue.Render(truncate(m.meta.OutputFile, w-12)) + "\n")
	}

	b.WriteString("\n")

	// Cassette shelf (batch mode only).  The shelf is the queue's
	// "video library wall" — sits above the deck so the VCR remains
	// the focal point.
	if m.shelf != nil {
		b.WriteString(m.shelf.View(w))
		b.WriteString("\n\n")
	}

	// Active panel — VCR while downloading, Mixer while muxing.  We
	// always render the VCR (it carries the metadata), and overlay the
	// Mixer below it once the post-processing phase begins.
	b.WriteString(centreBlock(m.vcr.View(), w))
	b.WriteString("\n")

	if m.phase == "muxing" || m.phase == "done" || m.phase == "error" {
		b.WriteString("\n")
		b.WriteString(centreBlock(m.mixer.View(), w))
		b.WriteString("\n")
	}

	// Log strip.
	if len(m.logs) > 0 {
		b.WriteString("\n")
		b.WriteString("  " + styleSectionChip.Render("EVENTS") + "\n")
		for _, e := range m.logs {
			ico, sty := logIconStyle(e.level)
			b.WriteString("  " + sty.Render(ico) + "  " + styleValue.Render(truncate(e.text, w-6)) + "\n")
		}
	}

	// Footer.
	b.WriteString("\n" + sep + "\n")
	switch {
	case m.stopping:
		b.WriteString("  " + styleHelp.Render("press ") +
			styleKeyCap.Render("q") +
			styleHelp.Render(" again to force-quit"))
	case m.done:
		b.WriteString("  " + styleDone.Render("◆ extraction complete") +
			styleHelp.Render(" — closing in 3s"))
	case m.hasError:
		b.WriteString("  " + styleError.Render("✗ ") +
			styleErrBox.Render(truncate(m.errMsg, w-10)))
	case m.browsing && !m.hasSelection:
		b.WriteString("  " +
			styleKeyCap.Render("▲▼") + styleHelp.Render(" tape   ") +
			styleKeyCap.Render("◀▶") + styleHelp.Render(" audio   ") +
			styleKeyCap.Render("⇥") + styleHelp.Render(" format   ") +
			styleKeyCap.Render("↵") + styleHelp.Render(" rec   ") +
			styleKeyCap.Render("q") + styleHelp.Render(" abort"))
	default:
		b.WriteString("  " + styleHelp.Render("press ") +
			styleKeyCap.Render("q") +
			styleHelp.Render(" to abort and exit"))
	}

	return b.String()
}

// centreBlock pads a multi-line block to centre it within terminal width.
func centreBlock(block string, termW int) string {
	lines := strings.Split(block, "\n")
	maxW := 0
	for _, ln := range lines {
		if w := lipgloss.Width(ln); w > maxW {
			maxW = w
		}
	}
	pad := (termW - maxW) / 2
	if pad < 0 {
		pad = 0
	}
	prefix := strings.Repeat(" ", pad)
	for i, ln := range lines {
		lines[i] = prefix + ln
	}
	return strings.Join(lines, "\n")
}

// ── Run loop ─────────────────────────────────────────────────────────────────

// ExtractorSelector blocks until the user commits a format selection
// from the VCR's browsing mode, or until ctx is cancelled.  Returns
// the full ExtractorSelectionMsg so callers can pull both the exact
// spec (for single-URL runs) and the adaptive descriptors (for batch
// FormatAll caching).  A zero-value selection means "use the default
// pipeline" — happens when the source has no selectable formats or
// the user dismissed without picking.
type ExtractorSelector func(ctx context.Context) (ExtractorSelectionMsg, error)

// ExtractorRunOptions configures RunExtractorTUI.
type ExtractorRunOptions struct {
	Ctx    context.Context
	URL    string
	OnQuit func()
}

// RunExtractorTUI starts a Bubble Tea program using the extractor model
// and runs `worker` in a goroutine.  The worker is expected to send
// Extractor* messages via the package-level Program handle.  Mirrors the
// shape of RunWithTUI: when stdout isn't a TTY, the worker runs directly
// and TUI sends are no-ops (the package-level Program is nil).
//
// `worker` is invoked with an ExtractorSelector that blocks until the
// user commits a format choice in the VCR's browsing mode.  Workers
// that bypass the selector (live streams, no format table) simply
// don't call it.  In the non-TTY fallback path the selector returns
// immediately with empty strings ("use defaults").
func RunExtractorTUI(opts ExtractorRunOptions, worker func(ExtractorSelector) error) error {
	if !(isatty.IsTerminal(os.Stdout.Fd()) && DisplayProgress) {
		// Non-TTY fallback — run the worker directly, log progress
		// through charmbracelet/log via ui.Printf().  Program stays nil
		// so the extractor sink drops UI events.  The selector returns
		// the zero value so yt-dlp's bv*+ba/b default kicks in.
		return worker(func(ctx context.Context) (ExtractorSelectionMsg, error) {
			return ExtractorSelectionMsg{}, nil
		})
	}

	selectionCh := make(chan ExtractorSelectionMsg, 1)
	model := NewExtractorModel(opts.URL, opts.OnQuit)
	model.SetSelectionCallback(func(s ExtractorSelectionMsg) {
		// Buffered + drop-if-full: a re-press of REC is a no-op.
		select {
		case selectionCh <- s:
		default:
		}
	})
	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithoutSignalHandler())
	Program = p

	selector := func(ctx context.Context) (ExtractorSelectionMsg, error) {
		select {
		case s := <-selectionCh:
			return s, nil
		case <-ctx.Done():
			return ExtractorSelectionMsg{}, ctx.Err()
		}
	}

	// External-cancel watchdog.  Mirrors RunWithTUI so SIGINT propagates
	// uniformly across both pipelines.
	stopWatch := make(chan struct{})
	if opts.Ctx != nil {
		go func() {
			select {
			case <-opts.Ctx.Done():
				// Switch the VCR/Mixer into eject/error mode and request quit.
				p.Send(ExtractorErrorMsg{Err: context.Cause(opts.Ctx)})
			case <-stopWatch:
			}
		}()
	}

	workerDone := make(chan struct{})
	var workerErr error
	go func() {
		defer close(workerDone)
		defer func() {
			if r := recover(); r != nil {
				if err, ok := r.(error); ok {
					workerErr = err
				} else {
					workerErr = fmt.Errorf("%v", r)
				}
			}
			if workerErr != nil {
				p.Send(ExtractorErrorMsg{Err: workerErr})
			} else {
				p.Send(ExtractorPhaseMsg{Phase: "done"})
			}
		}()
		workerErr = worker(selector)
	}()

	if _, err := p.Run(); err != nil {
		Program = nil
		close(stopWatch)
		<-workerDone
		if workerErr != nil {
			return fmt.Errorf("extractor TUI: %w (extractor error: %v)", err, workerErr)
		}
		return fmt.Errorf("extractor TUI: %w", err)
	}
	Program = nil
	close(stopWatch)
	<-workerDone
	return workerErr
}
