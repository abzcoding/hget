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

	onQuit func()
	startT time.Time
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
		phase:   "downloading",
		maxLogs: 5,
		onQuit:  onQuit,
		startT:  time.Now(),
	}
}

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
		return m, extractorTickCmd()

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

	case ExtractorPhaseMsg:
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
func RunExtractorTUI(opts ExtractorRunOptions, worker func() error) error {
	if !(isatty.IsTerminal(os.Stdout.Fd()) && DisplayProgress) {
		// Non-TTY fallback — run the worker directly, log progress
		// through charmbracelet/log via ui.Printf().  Program stays nil
		// so the extractor sink drops UI events.
		return worker()
	}

	model := NewExtractorModel(opts.URL, opts.OnQuit)
	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithoutSignalHandler())
	Program = p

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
		workerErr = worker()
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
