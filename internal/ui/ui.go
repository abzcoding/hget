package ui

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/harmonica"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	charmlog "github.com/charmbracelet/log"
	"github.com/mattn/go-colorable"
	"github.com/mattn/go-isatty"
)

// DisplayProgress controls whether the TUI progress bar is shown.
// Set to false in tests to disable TUI output.
var DisplayProgress = true

// ── Colors ────────────────────────────────────────────────────────────────────

var (
	colorPurple = lipgloss.Color("#C77DFF")
	colorCyan   = lipgloss.Color("#00B4D8")
	colorGreen  = lipgloss.Color("#06D6A0")
	colorYellow = lipgloss.Color("#FFB703")
	colorRed    = lipgloss.Color("#EF233C")
	colorMuted  = lipgloss.Color("#6C757D")
	colorBorder = lipgloss.Color("#495057")
	colorWhite  = lipgloss.Color("#F8F9FA")
)

// ── Styles ────────────────────────────────────────────────────────────────────

var (
	styleBanner = lipgloss.NewStyle().
			Foreground(colorPurple).
			Bold(true)

	styleLabel = lipgloss.NewStyle().
			Foreground(colorMuted).
			Width(8)

	styleValue = lipgloss.NewStyle().Foreground(colorCyan)
	styleSep   = lipgloss.NewStyle().Foreground(colorBorder)

	styleLogInfo  = lipgloss.NewStyle().Foreground(colorCyan)
	styleLogWarn  = lipgloss.NewStyle().Foreground(colorYellow)
	styleLogError = lipgloss.NewStyle().Foreground(colorRed)

	stylePartLabel = lipgloss.NewStyle().
			Foreground(colorPurple).
			Width(5)

	styleSpeed = lipgloss.NewStyle().
			Foreground(colorGreen).
			Width(14)

	styleHelp      = lipgloss.NewStyle().Foreground(colorMuted)
	styleHelpKey   = lipgloss.NewStyle().Foreground(colorPurple).Bold(true)
	styleDone      = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
	styleETA       = lipgloss.NewStyle().Foreground(colorYellow)
	styleError     = lipgloss.NewStyle().Foreground(colorRed).Bold(true)
	styleVerifyOK  = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
	styleVerifyBad = lipgloss.NewStyle().Foreground(colorRed).Bold(true)
	styleErrBox    = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorRed).
			Padding(0, 2).
			Foreground(colorWhite)
	styleStopBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorYellow).
			Padding(0, 2).
			Foreground(colorYellow).
			Bold(true)
	styleSkipBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorCyan).
			Padding(0, 2).
			Foreground(colorCyan).
			Bold(true)
)

const banner = `  ██╗  ██╗  ██████╗  ███████╗ ████████╗
  ██║  ██║ ██╔════╝  ██╔════╝ ╚══██╔══╝
  ███████║ ██║  ███╗ █████╗      ██║
  ██╔══██║ ██║   ██║ ██╔══╝      ██║
  ██║  ██║ ╚██████╔╝ ███████╗    ██║     fast multi-connection downloader
  ╚═╝  ╚═╝  ╚═════╝  ╚══════╝   ╚═╝`

// ── Tea messages ──────────────────────────────────────────────────────────────

// DownloadStartMsg is sent once download metadata is known.
type DownloadStartMsg struct {
	URL      string
	FileName string
	Size     int64
	NumParts int
	IPs      []string
}

// PartProgressMsg carries incremental progress for one chunk.
type PartProgressMsg struct {
	Index      int
	Downloaded int64
	Total      int64
}

// PartDoneMsg signals a chunk finished downloading successfully.
type PartDoneMsg struct{ Index int }

// JoinStartMsg signals the file-joining phase started.
type JoinStartMsg struct{ Total int }

// JoinProgressMsg carries joining progress.
type JoinProgressMsg struct{ Current int }

// JoinDoneMsg signals joining finished.
type JoinDoneMsg struct{}

// LogMsg adds an entry to the on-screen log panel.
type LogMsg struct {
	Level string // "info" | "warn" | "error"
	Text  string
}

// DownloadDoneMsg signals the entire pipeline (download + join + optional verify) finished.
type DownloadDoneMsg struct{}

// DownloadErrorMsg signals a fatal download error.
type DownloadErrorMsg struct{ Err error }

// VerifyStartMsg signals GPG signature verification has begun.
type VerifyStartMsg struct{}

// VerifyDoneMsg signals GPG verification has completed.
type VerifyDoneMsg struct {
	OK     bool
	Detail string // gpg output excerpt
}

// tickMsg drives periodic speed recalculation and spring animation.
type tickMsg time.Time

// autoQuitMsg is sent after the completion delay to quit the TUI.
type autoQuitMsg struct{}

// StoppingMsg is sent when an external cancellation (e.g. SIGINT routed
// through signal.NotifyContext) has been requested.  The TUI overlays a
// "stopping" panel until the worker goroutine reports completion.
type StoppingMsg struct {
	// Reason renders inside the stopping panel; e.g. "Aborted by user".
	Reason string
}

// SkippingMsg overlays a "skipping" panel for the current batch item.
type SkippingMsg struct{}

// ── Per-part model ────────────────────────────────────────────────────────────

type partModel struct {
	total      int64
	downloaded int64
	done       bool
	bar        progress.Model
	lastBytes  int64
	lastTime   time.Time
	speed      float64 // rolling EMA in bytes/sec

	// harmonica spring physics for smooth percentage animation
	spring    harmonica.Spring
	smoothPct float64
	springVel float64
}

// ── Log entry ─────────────────────────────────────────────────────────────────

type logEntry struct {
	level string
	text  string
}

// ── TUI model ─────────────────────────────────────────────────────────────────

type tuiModel struct {
	// download metadata
	url      string
	fileName string
	size     int64
	numConns int
	ips      []string

	// per-part state
	parts []partModel

	// overall progress — spring-smoothed
	overallBar    progress.Model
	startTime     time.Time
	totalDown     int64
	overallSpring harmonica.Spring
	overallPct    float64 // spring-smoothed overall %
	overallVel    float64

	// join phase — spring-smoothed
	joining     bool
	joinTotal   int
	joinCurrent int
	joinBar     progress.Model
	joinSpring  harmonica.Spring
	joinPct     float64
	joinVel     float64

	// log panel
	logs    []logEntry
	maxLogs int

	// batch context (0 = not in a batch)
	batchCurrent int // 1-based index of this download in the batch
	batchTotal   int // total downloads in the batch

	// lifecycle
	started    bool
	done       bool
	errMsg     string
	hasError   bool
	willVerify bool // set when --verify flag was requested

	// verification state (populated after download+join complete)
	verifying    bool
	verifyDone   bool
	verifyOK     bool
	verifyDetail string

	// cancellation hooks — invoked from the key handler.  onSkip is nil
	// when skipping is not allowed (single-download mode).
	onSkip func()
	onQuit func()

	// stopping/skipping overlays
	stopping       bool
	stoppingReason string
	skipping       bool

	// spinner (pre-start and verify)
	spinner spinner.Model

	// terminal width
	width int
}

// Program is the global tea.Program; goroutines call Program.Send() to deliver messages.
var Program *tea.Program

// NewTUIModel creates a new TUI model for the given number of connections.
// batchCurrent and batchTotal are 1-based; pass 0,0 when not in batch mode.
// onSkip is non-nil only in batch mode and is invoked when the user presses
// 's'.  onQuit is invoked on 'q' / 'ctrl+c'; both default to no-ops if nil.
func NewTUIModel(numConns int, willVerify bool, batchCurrent, batchTotal int, onSkip, onQuit func()) tuiModel {
	s := spinner.New()
	s.Spinner = spinner.Points
	s.Style = lipgloss.NewStyle().Foreground(colorPurple)
	return tuiModel{
		numConns:      numConns,
		maxLogs:       5,
		spinner:       s,
		width:         80,
		willVerify:    willVerify,
		batchCurrent:  batchCurrent,
		batchTotal:    batchTotal,
		overallSpring: harmonica.NewSpring(harmonica.FPS(60), 7.0, 0.85),
		joinSpring:    harmonica.NewSpring(harmonica.FPS(60), 7.0, 0.85),
		onSkip:        onSkip,
		onQuit:        onQuit,
	}
}

func (m tuiModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, tickCmd())
}

func tickCmd() tea.Cmd {
	return tea.Tick(16*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func autoQuitCmd() tea.Cmd {
	return tea.Tick(3*time.Second, func(t time.Time) tea.Msg { return autoQuitMsg{} })
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		bw := calcBarWidth(m.width)
		for i := range m.parts {
			m.parts[i].bar = newPartBar(bw)
		}
		if m.started {
			m.overallBar = newOverallBar(bw)
		}
		if m.joining {
			m.joinBar = newJoinBar(bw)
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "Q", "ctrl+c":
			// First press: request a graceful cancellation and stay on
			// screen so the user sees "Stopping…" while state is saved.
			// Second press: hard-quit the TUI immediately.
			if m.stopping {
				return m, tea.Quit
			}
			m.stopping = true
			m.stoppingReason = "Aborted by user — saving state"
			if m.onQuit != nil {
				m.onQuit()
			}
			return m, m.spinner.Tick
		case "s", "S":
			if m.onSkip == nil || m.skipping || m.stopping {
				return m, nil
			}
			m.skipping = true
			m.onSkip()
			return m, m.spinner.Tick
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tickMsg:
		now := time.Time(msg)
		for i := range m.parts {
			p := &m.parts[i]

			// Speed EMA.
			if !p.done {
				dt := now.Sub(p.lastTime).Seconds()
				if dt > 0.1 {
					db := p.downloaded - p.lastBytes
					newSpeed := float64(db) / dt
					if p.speed == 0 {
						p.speed = newSpeed
					} else {
						p.speed = 0.75*p.speed + 0.25*newSpeed
					}
					p.lastBytes = p.downloaded
					p.lastTime = now
				}
			}

			// Spring-smooth the displayed percentage.
			targetPct := 0.0
			if p.total > 0 {
				targetPct = math.Min(float64(p.downloaded)/float64(p.total), 1.0)
			}
			if p.done {
				targetPct = 1.0
			}
			p.smoothPct, p.springVel = p.spring.Update(p.smoothPct, p.springVel, targetPct)
			if p.smoothPct < 0 {
				p.smoothPct = 0
			}
		}

		// Overall spring.
		overallTarget := 0.0
		if m.size > 0 {
			overallTarget = math.Min(float64(m.totalDown)/float64(m.size), 1.0)
		}
		m.overallPct, m.overallVel = m.overallSpring.Update(m.overallPct, m.overallVel, overallTarget)
		if m.overallPct < 0 {
			m.overallPct = 0
		}

		// Join spring.
		joinTarget := 0.0
		if m.joinTotal > 0 {
			joinTarget = math.Min(float64(m.joinCurrent)/float64(m.joinTotal), 1.0)
		}
		m.joinPct, m.joinVel = m.joinSpring.Update(m.joinPct, m.joinVel, joinTarget)
		if m.joinPct < 0 {
			m.joinPct = 0
		}

		return m, tickCmd()

	case DownloadStartMsg:
		m.url = msg.URL
		m.fileName = msg.FileName
		m.size = msg.Size
		m.ips = msg.IPs
		m.startTime = time.Now()
		m.started = true
		bw := calcBarWidth(m.width)
		m.parts = make([]partModel, msg.NumParts)
		for i := range m.parts {
			m.parts[i] = partModel{
				bar:      newPartBar(bw),
				lastTime: time.Now(),
				spring:   harmonica.NewSpring(harmonica.FPS(60), 7.0, 0.85),
			}
		}
		m.overallBar = newOverallBar(bw)
		return m, nil

	case PartProgressMsg:
		if msg.Index >= 0 && msg.Index < len(m.parts) {
			m.parts[msg.Index].downloaded = msg.Downloaded
			m.parts[msg.Index].total = msg.Total
		}
		m.totalDown = 0
		for _, p := range m.parts {
			m.totalDown += p.downloaded
		}
		return m, nil

	case PartDoneMsg:
		if msg.Index >= 0 && msg.Index < len(m.parts) {
			m.parts[msg.Index].done = true
			m.parts[msg.Index].speed = 0
		}
		return m, nil

	case JoinStartMsg:
		m.joining = true
		m.joinTotal = msg.Total
		m.joinBar = newJoinBar(calcBarWidth(m.width))
		return m, nil

	case JoinProgressMsg:
		m.joinCurrent = msg.Current
		return m, nil

	case JoinDoneMsg:
		m.joining = false
		return m, nil

	case LogMsg:
		m.logs = append(m.logs, logEntry{level: msg.Level, text: msg.Text})
		if len(m.logs) > m.maxLogs {
			m.logs = m.logs[len(m.logs)-m.maxLogs:]
		}
		return m, nil

	case VerifyStartMsg:
		m.verifying = true
		return m, m.spinner.Tick

	case VerifyDoneMsg:
		m.verifying = false
		m.verifyDone = true
		m.verifyOK = msg.OK
		m.verifyDetail = msg.Detail
		return m, nil

	case StoppingMsg:
		if !m.stopping {
			m.stopping = true
			if msg.Reason != "" {
				m.stoppingReason = msg.Reason
			} else {
				m.stoppingReason = "Stopping — saving state"
			}
		}
		return m, m.spinner.Tick

	case SkippingMsg:
		if !m.skipping {
			m.skipping = true
		}
		return m, m.spinner.Tick

	case DownloadDoneMsg:
		m.done = true
		return m, autoQuitCmd()

	case DownloadErrorMsg:
		m.hasError = true
		if msg.Err != nil {
			m.errMsg = msg.Err.Error()
		} else {
			m.errMsg = "unknown error"
		}
		return m, autoQuitCmd()

	case autoQuitMsg:
		return m, tea.Quit
	}
	return m, nil
}

// ── Bar constructors ──────────────────────────────────────────────────────────

func newPartBar(w int) progress.Model {
	return progress.New(
		progress.WithGradient("#7B2FBE", "#00B4D8"),
		progress.WithWidth(w),
		progress.WithoutPercentage(),
	)
}

func newOverallBar(w int) progress.Model {
	return progress.New(
		progress.WithGradient("#C77DFF", "#06D6A0"),
		progress.WithWidth(w),
		progress.WithoutPercentage(),
	)
}

func newJoinBar(w int) progress.Model {
	return progress.New(
		progress.WithGradient("#FFB703", "#06D6A0"),
		progress.WithWidth(w),
		progress.WithoutPercentage(),
	)
}

func calcBarWidth(termWidth int) int {
	w := termWidth - 28
	if w < 20 {
		w = 20
	}
	if w > 60 {
		w = 60
	}
	return w
}

func sepWidth(termWidth int) int {
	w := termWidth - 2
	if w > 72 {
		return 72
	}
	return w
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m tuiModel) View() string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	sep := styleSep.Render(strings.Repeat("─", sepWidth(w)))

	b.WriteString(styleBanner.Render(banner))
	b.WriteString("\n\n")

	// Batch progress indicator.
	if m.batchTotal > 1 {
		doneSoFar := m.batchCurrent - 1
		remaining := m.batchTotal - m.batchCurrent
		batchLine := fmt.Sprintf("  File %d of %d", m.batchCurrent, m.batchTotal)
		if doneSoFar > 0 {
			batchLine += fmt.Sprintf("  ·  %s", styleDone.Render(fmt.Sprintf("%d done", doneSoFar)))
		}
		if remaining > 0 {
			batchLine += fmt.Sprintf("  ·  %s", styleValue.Render(fmt.Sprintf("%d left", remaining)))
		}
		b.WriteString(lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorPurple).
			Padding(0, 1).
			Foreground(colorMuted).
			Render(batchLine) + "\n\n")
	}

	// Pre-start spinner.
	if !m.started && !m.hasError {
		b.WriteString("  " + m.spinner.View() + "  Resolving…\n")
		if m.stopping || m.skipping {
			b.WriteString("\n" + m.renderStopOverlay() + "\n")
		}
		b.WriteString(sep + "\n")
		b.WriteString(m.renderFooter())
		return b.String()
	}

	// Error screen.
	if m.hasError {
		b.WriteString("  " + styleError.Render("✗  Download failed") + "\n\n")
		b.WriteString("  " + styleErrBox.Render(m.errMsg) + "\n\n")
		b.WriteString(sep + "\n")
		b.WriteString(styleHelp.Render("  closing in 3 s  •  ") + styleHelpKey.Render("q") + styleHelp.Render(" quit now"))
		return b.String()
	}

	// Metadata row.
	urlDisplay := m.url
	if maxU := w - 14; len(urlDisplay) > maxU && maxU > 4 {
		urlDisplay = urlDisplay[:maxU-1] + "…"
	}
	b.WriteString(styleLabel.Render("  URL") + "   " + styleValue.Render(urlDisplay) + "\n")
	b.WriteString(styleLabel.Render("  File") + "  " + styleValue.Render(m.fileName) + "\n")
	b.WriteString(styleLabel.Render("  Size") + "  " + styleValue.Render(formatBytes(m.size)) + "\n")
	b.WriteString(styleLabel.Render("  Conns") + " " + styleValue.Render(fmt.Sprintf("%d", m.numConns)) + "\n")
	if len(m.ips) > 0 {
		b.WriteString(styleLabel.Render("  IPs") + "   " + styleValue.Render(strings.Join(m.ips, " · ")) + "\n")
	}
	b.WriteString("\n" + sep + "\n\n")

	// Completion screen.
	if m.done && !m.joining {
		elapsed := time.Since(m.startTime)
		var avg float64
		if s := elapsed.Seconds(); s > 0 {
			avg = float64(m.size) / s
		}
		b.WriteString("  " + styleDone.Render("✓  Download complete!") + "\n\n")
		b.WriteString(styleLabel.Render("  Time") + "   " + styleValue.Render(formatDuration(elapsed)) + "\n")
		b.WriteString(styleLabel.Render("  Avg") + "    " + styleValue.Render(formatSpeed(avg)) + "\n")
		b.WriteString(styleLabel.Render("  Saved") + "  " + styleValue.Render(m.fileName) + "\n")
		// Verification result row.
		if m.willVerify {
			b.WriteString(styleLabel.Render("  Sig") + "    ")
			if m.verifyDone {
				if m.verifyOK {
					b.WriteString(styleVerifyOK.Render("✓ Valid") + "\n")
				} else {
					b.WriteString(styleVerifyBad.Render("✗ Invalid") + "\n")
					if m.verifyDetail != "" {
						lines := strings.SplitN(strings.TrimSpace(m.verifyDetail), "\n", 3)
						for _, l := range lines {
							b.WriteString("         " + styleLogError.Render(truncate(l, w-11)) + "\n")
						}
					}
				}
			} else {
				b.WriteString(m.spinner.View() + "  Verifying…\n")
			}
		}
		b.WriteString("\n" + sep + "\n")
		b.WriteString(styleHelp.Render("  closing in 3 s  •  ") + styleHelpKey.Render("q") + styleHelp.Render(" quit now"))
		return b.String()
	}

	// Verifying phase (download+join complete, waiting for GPG result).
	if m.verifying && !m.done {
		b.WriteString("  " + m.spinner.View() + "  Verifying GPG signature…\n")
		if m.stopping || m.skipping {
			b.WriteString("\n" + m.renderStopOverlay() + "\n")
		}
		b.WriteString(sep + "\n")
		b.WriteString(m.renderFooter())
		return b.String()
	}

	// Join phase.
	if m.joining {
		pct := math.Min(m.joinPct, 1.0)
		b.WriteString("  " + stylePartLabel.Render("Join") + "  ")
		b.WriteString(m.joinBar.ViewAs(pct))
		b.WriteString(fmt.Sprintf("  %5.1f%%\n", pct*100))
		b.WriteString("\n" + sep + "\n")
	} else {
		// Per-part rows.
		for i, p := range m.parts {
			pct := math.Max(0, math.Min(p.smoothPct, 1.0))
			rawPct := 0.0
			if p.total > 0 {
				rawPct = math.Min(float64(p.downloaded)/float64(p.total), 1.0)
			}
			if p.done {
				rawPct = 1.0
			}
			label := fmt.Sprintf("#%d", i+1)
			b.WriteString("  " + stylePartLabel.Render(label) + " ")
			b.WriteString(p.bar.ViewAs(pct))
			b.WriteString(fmt.Sprintf("  %5.1f%%", rawPct*100))
			if p.done {
				b.WriteString("  " + styleDone.Render("✓"))
			} else {
				b.WriteString("  " + styleSpeed.Render(formatSpeed(p.speed)))
			}
			b.WriteString("\n")
		}

		// Overall bar.
		if m.size > 0 {
			b.WriteString("\n" + sep + "\n\n")
			pct := math.Max(0, math.Min(m.overallPct, 1.0))
			rawPct := math.Min(float64(m.totalDown)/float64(m.size), 1.0)

			// Total download speed.
			var totalSpeed float64
			for _, p := range m.parts {
				totalSpeed += p.speed
			}

			b.WriteString("  " + stylePartLabel.Render("Total") + " ")
			b.WriteString(m.overallBar.ViewAs(pct))
			b.WriteString(fmt.Sprintf("  %5.1f%%", rawPct*100))

			if rawPct > 0.001 {
				elapsed := time.Since(m.startTime).Seconds()
				eta := elapsed/rawPct - elapsed
				if eta > 0 {
					b.WriteString("  ETA " + styleETA.Render(formatDuration(time.Duration(eta*float64(time.Second)))))
				}
				if totalSpeed > 0 {
					b.WriteString("  " + styleSpeed.Render(formatSpeed(totalSpeed)))
				}
			}
			b.WriteString("\n")
		}
	}

	// Log panel.
	if len(m.logs) > 0 {
		b.WriteString("\n" + sep + "\n")
		for _, l := range m.logs {
			icon, st := logIconStyle(l.level)
			b.WriteString("  " + st.Render(icon+" "+truncate(l.text, w-6)) + "\n")
		}
	}

	// Stopping / skipping overlay (rendered above the footer so it stays
	// visible while we wait for the worker goroutine to finish saving state).
	if m.stopping || m.skipping {
		b.WriteString("\n" + m.renderStopOverlay() + "\n")
	}

	// Footer.
	b.WriteString("\n" + sep + "\n")
	b.WriteString(m.renderFooter())
	return b.String()
}

// renderStopOverlay produces the yellow/cyan boxed "stopping…" / "skipping…"
// banner shown while the worker goroutine is still draining.  It mirrors the
// styling of the verify completion box so the UI feels consistent.
func (m tuiModel) renderStopOverlay() string {
	if m.skipping && !m.stopping {
		text := "  " + m.spinner.View() + "  Skipping current download — discarding parts"
		return styleSkipBox.Render(text)
	}
	reason := m.stoppingReason
	if reason == "" {
		reason = "Stopping — saving state"
	}
	text := "  " + m.spinner.View() + "  " + reason
	return styleStopBox.Render(text)
}

// renderFooter renders the bottom help bar.  Keys are colored differently from
// their descriptions so the available actions read at a glance.
func (m tuiModel) renderFooter() string {
	var parts []string
	if m.stopping {
		parts = append(parts, styleHelp.Render("press ")+styleHelpKey.Render("q")+styleHelp.Render(" again to force-quit"))
	} else {
		if m.onSkip != nil {
			parts = append(parts, styleHelpKey.Render("s")+styleHelp.Render(" skip item"))
		}
		parts = append(parts, styleHelpKey.Render("q")+styleHelp.Render(" stop & save"))
		parts = append(parts, styleHelpKey.Render("ctrl+c")+styleHelp.Render(" abort"))
	}
	return "  " + strings.Join(parts, styleHelp.Render("  •  "))
}

func logIconStyle(level string) (string, lipgloss.Style) {
	switch level {
	case "warn":
		return "⚠", styleLogWarn
	case "error":
		return "✗", styleLogError
	default:
		return "ℹ", styleLogInfo
	}
}

func truncate(s string, maxLen int) string {
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}

func formatBytes(b int64) string {
	if b <= 0 {
		return "unknown"
	}
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func formatSpeed(bps float64) string {
	if bps <= 0 {
		return "─"
	}
	return fmt.Sprintf("↓ %s/s", formatBytes(int64(bps)))
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	if h > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

// ── Console logging ───────────────────────────────────────────────────────────

var (
	Stdout    = colorable.NewColorableStdout()
	Stderr    = colorable.NewColorableStderr()
	DefaultUI = Console{Stdout: Stdout, Stderr: Stderr}

	// Log is the global structured console logger used when the TUI is inactive.
	// It uses charmbracelet/log with custom lipgloss styles matching the TUI palette.
	Log *charmlog.Logger
)

func init() {
	Log = charmlog.NewWithOptions(Stdout, charmlog.Options{
		ReportTimestamp: false,
	})
	styles := charmlog.DefaultStyles()
	styles.Levels[charmlog.InfoLevel] = lipgloss.NewStyle().
		SetString(" INFO").
		Padding(0, 1, 0, 1).
		Foreground(colorCyan).
		Bold(true)
	styles.Levels[charmlog.WarnLevel] = lipgloss.NewStyle().
		SetString(" WARN").
		Padding(0, 1, 0, 1).
		Foreground(colorYellow).
		Bold(true)
	styles.Levels[charmlog.ErrorLevel] = lipgloss.NewStyle().
		SetString("ERROR").
		Padding(0, 1, 0, 1).
		Foreground(colorRed).
		Bold(true)
	Log.SetStyles(styles)
}

// UI represents simple IO output.
type UI interface {
	Printf(format string, a ...any) (n int, err error)
	Println(a ...any) (n int, err error)
	Errorf(format string, a ...any) (n int, err error)
	Errorln(a ...any) (n int, err error)
}

// Printf outputs information-level logs, routing to TUI when available.
func Printf(format string, a ...any) (n int, err error) {
	msg := fmt.Sprintf(format, a...)
	trimmed := strings.TrimRight(msg, "\n")
	if Program != nil {
		Program.Send(LogMsg{Level: "info", Text: trimmed})
		return len(msg), nil
	}
	Log.Info(trimmed)
	return len(msg), nil
}

// Errorf outputs error-level logs, routing to TUI when available.
func Errorf(format string, a ...any) (n int, err error) {
	msg := fmt.Sprintf(format, a...)
	trimmed := strings.TrimRight(msg, "\n")
	if Program != nil {
		Program.Send(LogMsg{Level: "error", Text: trimmed})
		return len(msg), nil
	}
	Log.Error(trimmed)
	return len(msg), nil
}

// Warnf outputs warning-level logs, routing to TUI when available.
func Warnf(format string, a ...any) (n int, err error) {
	msg := fmt.Sprintf(format, a...)
	trimmed := strings.TrimRight(msg, "\n")
	if Program != nil {
		Program.Send(LogMsg{Level: "warn", Text: trimmed})
		return len(msg), nil
	}
	Log.Warn(trimmed)
	return len(msg), nil
}

// Errorln is the non-formatted error printer.
func Errorln(a ...any) (n int, err error) {
	msg := fmt.Sprint(a...)
	if Program != nil {
		Program.Send(LogMsg{Level: "error", Text: msg})
		return 0, nil
	}
	Log.Error(msg)
	return 0, nil
}

// IsTerminal checks if f is connected to a real TTY.
func IsTerminal(f *os.File) bool {
	return isatty.IsTerminal(f.Fd())
}

// DisplayProgressBar returns true when running in interactive TTY mode.
func DisplayProgressBar() bool {
	return isatty.IsTerminal(os.Stdout.Fd()) && DisplayProgress
}

// NewProgram creates and starts a new Bubble Tea program for the TUI.
func NewProgram(numConns int) *tea.Program {
	model := NewTUIModel(numConns, false, 0, 0, nil, nil)
	return tea.NewProgram(model, tea.WithAltScreen())
}

// Console is the non-TUI implementation of UI.
type Console struct {
	Stdout io.Writer
	Stderr io.Writer
}

func (c Console) Printf(format string, a ...any) (n int, err error) {
	return fmt.Fprintf(c.Stdout, format, a...)
}

func (c Console) Println(a ...any) (n int, err error) {
	return fmt.Fprintln(c.Stdout, a...)
}

func (c Console) Errorf(format string, a ...any) (n int, err error) {
	return fmt.Fprintf(c.Stderr, format, a...)
}

func (c Console) Errorln(a ...any) (n int, err error) {
	return fmt.Fprintln(c.Stderr, a...)
}

// ── High-level helpers used by cmd layer ──────────────────────────────────────

// RunOptions configures a TUI session.
type RunOptions struct {
	// Ctx is observed for external cancellation (e.g. SIGINT routed through
	// signal.NotifyContext at main).  When Ctx is cancelled, RunWithTUI
	// surfaces a "stopping…" overlay and waits for fn to drain.
	Ctx context.Context
	// OnSkip is called when the user presses 's' (batch mode only).
	OnSkip func()
	// OnQuit is called when the user presses 'q' / 'ctrl+c'.
	OnQuit       func()
	NumConns     int
	WillVerify   bool
	BatchCurrent int
	BatchTotal   int
}

// RunWithTUI starts a Bubble Tea program for interactive TTY sessions and runs
// fn in a background goroutine.  Falls back to plain execution when not in a
// TTY.  Returns the error returned by fn (or recovered from a panic inside
// it), so callers can distinguish skip vs abort vs failure.
func RunWithTUI(opts RunOptions, fn func() error) error {
	if isatty.IsTerminal(os.Stdout.Fd()) && DisplayProgress {
		model := NewTUIModel(opts.NumConns, opts.WillVerify, opts.BatchCurrent, opts.BatchTotal, opts.OnSkip, opts.OnQuit)
		// Disable bubbletea's built-in SIGINT handler so external signals are
		// handled by the parent context (signal.NotifyContext at main).  This
		// keeps cancellation routing single-source and lets us show a real
		// "stopping…" overlay instead of dropping the alt-screen.
		p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithoutSignalHandler())
		Program = p

		// Watch the parent context: if it gets cancelled (external SIGINT or
		// the caller decided to abort), surface the stopping overlay so the
		// user sees what's happening while state is being saved.
		stopWatch := make(chan struct{})
		if opts.Ctx != nil {
			go func() {
				select {
				case <-opts.Ctx.Done():
					reason := "Aborted — saving state"
					if cause := context.Cause(opts.Ctx); cause != nil && cause != opts.Ctx.Err() {
						reason = "Aborted (" + cause.Error() + ") — saving state"
					}
					p.Send(StoppingMsg{Reason: reason})
				case <-stopWatch:
				}
			}()
		}

		// fnDone is closed once fn() (and its defer) have fully completed.
		// We MUST wait for this before returning so that the next RunWithTUI call
		// cannot set ui.Program to a new value while the old download goroutines
		// are still alive and sending PartProgressMsg etc. into it.
		fnDone := make(chan struct{})
		var fnErr error
		go func() {
			defer close(fnDone)
			defer func() {
				if r := recover(); r != nil {
					if err, ok := r.(error); ok {
						fnErr = err
					} else {
						fnErr = fmt.Errorf("%v", r)
					}
				}
				if fnErr != nil {
					p.Send(DownloadErrorMsg{Err: fnErr})
				} else {
					p.Send(DownloadDoneMsg{})
				}
			}()
			fnErr = fn()
		}()
		if _, err := p.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "TUI error:", err)
			os.Exit(1)
		}
		// Clear the global handle first so that any in-flight progress writes
		// from the goroutine see nil and stop sending immediately.
		Program = nil
		close(stopWatch)
		// Now wait for fn() to finish.  This blocks until Execute() returns and
		// all its child goroutines (downloadPart, dl.Do) have exited, guaranteeing
		// zero stale messages can reach the next TUI session.
		<-fnDone
		return fnErr
	}
	// Non-TTY: run directly.
	return fn()
}

// ConfirmRedownload shows a styled huh confirmation prompt asking whether to
// overwrite an existing file.  Returns true when the user says yes (or when
// stdout is not a terminal, in which case the download proceeds silently).
func ConfirmRedownload(filename string) bool {
	if !isatty.IsTerminal(os.Stdout.Fd()) {
		return true
	}
	labelStyle := lipgloss.NewStyle().Foreground(colorPurple).Bold(true)
	fileStyle := lipgloss.NewStyle().Foreground(colorCyan)

	fmt.Println()
	fmt.Println(labelStyle.Render("  ⚠  File already exists:") + " " + fileStyle.Render(filename))
	fmt.Println()

	var proceed bool
	f := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Download again and overwrite?").
				Value(&proceed).
				Affirmative("Yes, overwrite").
				Negative("No, keep existing"),
		),
	)
	_ = f.Run()
	return proceed
}

// PrintHelp renders a styled --help screen to stdout.
func PrintHelp() {
	// ── Banner ────────────────────────────────────────────────────────────────
	fmt.Fprintln(Stdout, styleBanner.Render(banner))
	fmt.Fprintln(Stdout)

	w := 68 // fixed help width
	sep := styleSep.Render(strings.Repeat("─", w))

	// ── Shared style helpers ──────────────────────────────────────────────────
	sectionHeader := lipgloss.NewStyle().
		Foreground(colorPurple).
		Bold(true).
		MarginLeft(2)

	flagName := lipgloss.NewStyle().
		Foreground(colorCyan).
		Bold(true).
		Width(20)

	flagDesc := lipgloss.NewStyle().
		Foreground(colorWhite)

	flagDefault := lipgloss.NewStyle().
		Foreground(colorMuted)

	usageLine := lipgloss.NewStyle().
		Foreground(colorGreen).
		MarginLeft(4)

	exampleLine := lipgloss.NewStyle().
		Foreground(colorCyan).
		MarginLeft(4)

	commentLine := lipgloss.NewStyle().
		Foreground(colorMuted).
		MarginLeft(4)

	// ── Usage ────────────────────────────────────────────────────────────────
	fmt.Fprintln(Stdout, sectionHeader.Render("USAGE"))
	fmt.Fprintln(Stdout, usageLine.Render("hget [options] <url>"))
	fmt.Fprintln(Stdout, usageLine.Render("hget [options] --resume=<task-name>"))
	fmt.Fprintln(Stdout, usageLine.Render("hget --file=<urls-file> [options]"))
	fmt.Fprintln(Stdout)
	fmt.Fprintln(Stdout, sep)
	fmt.Fprintln(Stdout)

	// ── Options ───────────────────────────────────────────────────────────────
	type opt struct{ flag, desc, def string }
	options := []opt{
		{"-n <int>", "number of parallel connections", "# of CPUs"},
		{"--skip-tls", "skip TLS certificate verification", "false"},
		{"--proxy <addr>", "proxy  (socks5: host:port  |  http: http://host:port)", ""},
		{"--file <path>", "path to a file containing one URL per line", ""},
		{"--rate <limit>", "bandwidth cap per download  (e.g. 10kB, 5MiB)", ""},
		{"--resume <task>", "resume a stopped download by task name or URL", ""},
		{"--probe <url>", "probe URL for range support & content-length only", ""},
		{"--timeout <dur>", "timeout waiting for response headers  (e.g. 30s, 1m)", "15s"},
		{"--verify", "download & GPG-verify the .sig signature file", "false"},
	}

	fmt.Fprintln(Stdout, sectionHeader.Render("OPTIONS"))
	for _, o := range options {
		line := "  " + flagName.Render(o.flag) + "  " + flagDesc.Render(o.desc)
		if o.def != "" {
			line += "  " + flagDefault.Render("(default: "+o.def+")")
		}
		fmt.Fprintln(Stdout, line)
	}
	fmt.Fprintln(Stdout)
	fmt.Fprintln(Stdout, sep)
	fmt.Fprintln(Stdout)

	// ── Examples ─────────────────────────────────────────────────────────────
	fmt.Fprintln(Stdout, sectionHeader.Render("EXAMPLES"))

	examples := []struct{ comment, cmd string }{
		{"basic download", "hget https://example.com/file.iso"},
		{"8 connections, 5 MiB/s cap", "hget -n 8 --rate 5MiB https://example.com/large.tar.gz"},
		{"resume an interrupted download", "hget --resume https://example.com/file.iso"},
		{"batch download from a file", "hget --file urls.txt"},
		{"probe server without downloading", "hget --probe https://example.com/file.iso"},
		{"download & verify GPG signature", "hget --verify https://example.com/file.iso"},
	}
	for _, e := range examples {
		fmt.Fprintln(Stdout, commentLine.Render("# "+e.comment))
		fmt.Fprintln(Stdout, exampleLine.Render(e.cmd))
		fmt.Fprintln(Stdout)
	}
}

// PrintVerifySummary writes a styled one-line verify result to the terminal
// using charmbracelet/log (works after the TUI alt-screen has closed).
func PrintVerifySummary(ok bool, detail string) {
	if ok {
		Printf("Signature valid — %s\n", detail)
	} else {
		Errorf("Signature invalid — %s\n", detail)
	}
}
