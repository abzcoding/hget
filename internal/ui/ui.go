package ui

import (
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

	styleHelp   = lipgloss.NewStyle().Foreground(colorMuted)
	styleDone   = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
	styleETA    = lipgloss.NewStyle().Foreground(colorYellow)
	styleError  = lipgloss.NewStyle().Foreground(colorRed).Bold(true)
	styleErrBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorRed).
			Padding(0, 2).
			Foreground(colorWhite)
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

// DownloadDoneMsg signals the entire pipeline (download + join) finished successfully.
type DownloadDoneMsg struct{}

// DownloadErrorMsg signals a fatal download error.
type DownloadErrorMsg struct{ Err error }

// tickMsg drives periodic speed recalculation and spring animation.
type tickMsg time.Time

// autoQuitMsg is sent after the completion delay to quit the TUI.
type autoQuitMsg struct{}

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

	// lifecycle
	started  bool
	done     bool
	errMsg   string
	hasError bool

	// spinner (pre-start)
	spinner spinner.Model

	// terminal width
	width int
}

// Program is the global tea.Program; goroutines call Program.Send() to deliver messages.
var Program *tea.Program

// NewTUIModel creates a new TUI model for the given number of connections.
func NewTUIModel(numConns int) tuiModel {
	s := spinner.New()
	s.Spinner = spinner.Points
	s.Style = lipgloss.NewStyle().Foreground(colorPurple)
	return tuiModel{
		numConns:      numConns,
		maxLogs:       5,
		spinner:       s,
		width:         80,
		overallSpring: harmonica.NewSpring(harmonica.FPS(60), 7.0, 0.85),
		joinSpring:    harmonica.NewSpring(harmonica.FPS(60), 7.0, 0.85),
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
		if msg.String() == "q" || msg.String() == "Q" || msg.String() == "ctrl+c" {
			return m, tea.Quit
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

	// Pre-start spinner.
	if !m.started && !m.hasError {
		b.WriteString("  " + m.spinner.View() + "  Resolving…\n")
		b.WriteString(sep + "\n")
		b.WriteString(styleHelp.Render("  q quit"))
		return b.String()
	}

	// Error screen.
	if m.hasError {
		b.WriteString("  " + styleError.Render("✗  Download failed") + "\n\n")
		b.WriteString("  " + styleErrBox.Render(m.errMsg) + "\n\n")
		b.WriteString(sep + "\n")
		b.WriteString(styleHelp.Render("  closing in 3 s  •  q quit now"))
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
		b.WriteString("\n" + sep + "\n")
		b.WriteString(styleHelp.Render("  closing in 3 s  •  q quit now"))
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

	// Footer.
	b.WriteString("\n" + sep + "\n")
	b.WriteString(styleHelp.Render("  q quit  •  ctrl+c abort"))
	return b.String()
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
	model := NewTUIModel(numConns)
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
