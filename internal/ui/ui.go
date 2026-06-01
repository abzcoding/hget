package ui

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/harmonica"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	charmlog "github.com/charmbracelet/log"
	"github.com/mattn/go-isatty"
)

// DisplayProgress controls whether the TUI progress bar is shown.
// Set to false in tests to disable TUI output.
var DisplayProgress = true

// ── Colors ────────────────────────────────────────────────────────────────────
//
// All colour values live in theme.go (ui.Theme).  The names below are
// short package-local aliases so renderers can write `colorPhosphor`
// without prefacing every reference with `Theme.`.  Aliases are kept so
// existing call-sites (banner, log icons, verify summary, batch styles)
// keep compiling.

var (
	colorPhosphor = Theme.Phosphor
	colorAmber    = Theme.Amber
	colorMint     = Theme.Mint
	colorMagenta  = Theme.Magenta
	colorSteel    = Theme.Steel
	colorSlate    = Theme.Slate
	colorFrost    = Theme.Frost
	colorDeepCyan = Theme.DeepCyan

	// Legacy hue aliases — phased out in favour of the carrier names above.
	colorPurple = colorPhosphor
	colorCyan   = colorPhosphor
	colorGreen  = colorMint
	colorYellow = colorAmber
	colorRed    = colorMagenta
	colorMuted  = colorSteel
	colorBorder = colorSlate
	colorWhite  = colorFrost
)

// ── Styles ────────────────────────────────────────────────────────────────────

var (
	styleBanner = lipgloss.NewStyle().
			Foreground(colorPurple).
			Bold(true)

	styleLabel = lipgloss.NewStyle().
			Foreground(colorSteel).
			Width(8).
			MarginRight(1)

	styleValue       = lipgloss.NewStyle().Foreground(colorFrost)
	styleAccentValue = lipgloss.NewStyle().Foreground(colorPhosphor).Bold(true)
	styleSep         = lipgloss.NewStyle().Foreground(colorSlate)

	styleLogInfo  = lipgloss.NewStyle().Foreground(colorCyan)
	styleLogWarn  = lipgloss.NewStyle().Foreground(colorYellow)
	styleLogError = lipgloss.NewStyle().Foreground(colorRed)

	stylePartLabel = lipgloss.NewStyle().
			Foreground(colorPurple).
			Width(5)

	styleSpeed = lipgloss.NewStyle().
			Foreground(colorGreen).
			Width(14)

	styleHelp    = lipgloss.NewStyle().Foreground(colorMuted)
	styleHelpKey = lipgloss.NewStyle().Foreground(colorPurple).Bold(true)
	// keyCap renders a key as a small rounded "pill" — like a tiny
	// keyboard cap.  It elevates footer affordances above plain bold text.
	styleKeyCap = lipgloss.NewStyle().
			Foreground(colorFrost).
			Background(colorSlate).
			Bold(true).
			Padding(0, 1)
	// sectionChip introduces named sections ("STREAMS", "TELEMETRY",
	// "EVENTS") with a tiny tracked-out rounded label.  It anchors the
	// hierarchy without consuming a full divider line.
	styleSectionChip = lipgloss.NewStyle().
				Foreground(colorPhosphor).
				Bold(true).
				Padding(0, 1).
				Border(lipgloss.RoundedBorder(), false, false, false, false).
				MarginRight(1)
	// statusPill — soft background-tinted badge for per-part state.
	stylePillDone = lipgloss.NewStyle().
			Foreground(colorMint).
			Background(lipgloss.Color("#0F2B22")).
			Bold(true).
			Padding(0, 1)
	stylePillActive = lipgloss.NewStyle().
			Foreground(colorAmber).
			Background(lipgloss.Color("#2B1F0F")).
			Bold(true).
			Padding(0, 1)
	stylePillIdle = lipgloss.NewStyle().
			Foreground(colorSteel).
			Background(lipgloss.Color("#1B2230")).
			Padding(0, 1)
	styleDone   = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
	styleETA    = lipgloss.NewStyle().Foreground(colorYellow)
	styleError  = lipgloss.NewStyle().Foreground(colorRed).Bold(true)
	styleErrBox = lipgloss.NewStyle().
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

// banner — refined "phosphor" wordmark. We commit to a single design
// language: half-block letterforms that read like a CRT-rendered monogram.
// The strapline beneath uses ▓▒░ shading to evoke a carrier signal fading
// in/out of the spectrum.  Three lines (vs the previous six) gives every
// terminal more vertical room for actual telemetry.
const banner = `  █░█ █▀▀ █▀▀ ▀█▀
  █▀█ █▄█ ██▄  █ `

const bannerStrap = `  ░▒▓ carrier signal · multi-stream telemetry · resumable ▓▒░`

// signalPulse — a custom spinner that mimics a carrier-wave amplitude meter.
// Eight Braille frames sweep upward, then back down, evoking a continuous
// signal pulse rather than the generic dots/lines from bubbles defaults.
var signalPulse = spinner.Spinner{
	Frames: []string{"⡀", "⡄", "⡆", "⡇", "⠇", "⠃", "⠁", "⠂"},
	FPS:    time.Second / 12,
}

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

// BatchItemBeginMsg signals the start of a new item inside a persistent
// batch TUI program.  The model resets per-item state but keeps the SAN
// cabinet, history, and program alive (no alt-screen toggling).
type BatchItemBeginMsg struct {
	Index    int // 0-based index into the batch
	URL      string
	FileName string
}

// BatchItemEndMsg signals an item finished (success / fail / skip / abort).
// Updates the SAN bay status without quitting the program.
type BatchItemEndMsg struct {
	Index  int
	Status BatchItemStatus
	Reason string
}

// BatchFinishedMsg signals all items have run; render the final summary
// frame and auto-quit.
type BatchFinishedMsg struct {
	Done    int
	Failed  int
	Skipped int
	Aborted int
}

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

	// rolling speed history for the total-bandwidth sparkline
	speedHistory []float64
	peakSpeed    float64

	// batch context (0 = not in a batch)
	batchCurrent int // 1-based index of this download in the batch
	batchTotal   int // total downloads in the batch
	// batchHistory mirrors RunOptions.BatchHistory: the parent batch
	// loop's lifecycle record for each item when this TUI session
	// started.  Used to seed the SAN cabinet's bay statuses so skipped /
	// failed bays don't masquerade as DONE.
	batchHistory []BatchItemSnapshot

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
	// skipped is the *terminal* skipped state — set when the worker
	// goroutine finished because the user pressed 's'.  Distinct from
	// `skipping` (transitional) which signals the request is in flight
	// while the worker drains.  When skipped is true we render a
	// dedicated "transfer severed" scene instead of the error screen.
	skipped bool

	// spinner (pre-start and verify)
	spinner spinner.Model

	// link — animated data-link / modem panel rendered as the centrepiece
	// of the download view; absorbs per-channel rows + aggregate bar so
	// nothing is duplicated below.
	link       dataLink
	modem      modemHandshake
	joinAnim   joinAnimation
	verifyAnim verifyAnimation

	// 90s mainframe-themed scene primitives.  Used at terminal widths
	// >= mainframeSceneMinWidth to wrap the data-link panel inside an
	// actual mainframe→cable→tape composition.  Below that threshold the
	// model falls back to the data-link panel alone.
	mf      mainframe
	bus     cable
	tape    tape
	sanView san
	hasSan  bool

	// terminal dimensions
	width  int
	height int

	// batchMode — set when this TUI session drives an entire batch via a
	// single persistent tea.Program.  When true, DownloadDoneMsg and
	// DownloadErrorMsg do NOT auto-quit; the wrapping goroutine moves to
	// the next item by sending BatchItemBeginMsg.
	batchMode bool

	// final batch summary, populated on BatchFinishedMsg.
	batchFinished                                                          bool
	batchFinalDone, batchFinalFailed, batchFinalSkipped, batchFinalAborted int
}

// ── Layout tiers ──────────────────────────────────────────────────────────────
//
// The download view composes several boxes that each have a fixed minimum
// height: data-link panel (~12), tape banner (9), mainframe (17), plus
// metadata header and footer chrome (~10).  At low terminal heights the
// composed view overruns the screen.  The tier system detects the
// available room and progressively downgrades the visualisation:
//
//   tierFull         — vertical stack: mainframe → cable → tape → data-link
//   tierSideBySide   — mainframe alongside tape (saves ~12 rows of height
//                      but needs ~138 cells of width)
//   tierTapeOnly     — tape banner only (no mainframe), then data-link
//   tierMinimal      — data-link panel alone
//
// Mainframe + tape composition is purely chrome; the data-link panel is
// the canonical telemetry surface and is never dropped.

type layoutTier int

const (
	tierMinimal    layoutTier = iota // data-link only
	tierTapeOnly                     // tape banner + data-link
	tierSideBySide                   // mainframe + tape side-by-side + data-link
	tierFull                         // full vertical stack
)

// chromeMinHeight estimates the rows consumed by everything that is NOT
// the mainframe scene + data-link panel: metadata block (~5), separators
// (~2), sparkline (~2), footer (~2).  Used to decide how much room is
// left for the scene primitives.
const chromeMinHeight = 10

// computeTier picks the richest layout that fits the current terminal.
func (m tuiModel) computeTier() layoutTier {
	// Treat unknown size as "full" so non-TTY snapshot tests still work.
	if m.width == 0 || m.height == 0 {
		return tierFull
	}
	// Available rows for the scene + datalink panel together.
	chrome := chromeMinHeight
	if m.batchTotal > 1 {
		chrome += 2 // batch counter banner adds two rows
	}
	avail := m.height - chrome
	dataLinkH := len(m.parts) + 8 // headers + per-part rows + agg + borders
	if dataLinkH < 12 {
		dataLinkH = 12
	}
	sceneBudget := avail - dataLinkH

	// Batch downloads use the SAN cabinet instead of mainframe+cable+tape.
	// SAN height varies with the number of bay rows we can pack in; we
	// prefer the chassis-on variant ("full") and fall back to compact
	// (no chassis) when only a single bay row fits.
	if m.batchTotal > 1 {
		// SAN minimum widths: 1 bay = 24 cells inner, 2 bays = 48, 3 = 70.
		// Allow tier=Full when at least 1 bay fits with chassis.
		switch {
		case fittingBayRows(sceneBudget, true) >= 1 && m.width >= mainframeSceneMinWidth:
			return tierFull
		case fittingBayRows(sceneBudget, false) >= 1 && m.width >= mainframeSceneMinWidth:
			return tierTapeOnly
		default:
			return tierMinimal
		}
	}

	// Single-download tier thresholds.
	const (
		mainframeH        = 17
		cableH            = 3
		tapeH             = 9
		sideBySideMinW    = 138
		fullStackMinW     = mainframeSceneMinWidth
		mainframeMinScene = mainframeH + cableH + tapeH // 29
		tapeOnlyMinScene  = tapeH                       // 9
	)

	switch {
	case avail >= mainframeMinScene+dataLinkH && m.width >= fullStackMinW:
		return tierFull
	case avail >= mainframeH+dataLinkH && m.width >= sideBySideMinW:
		return tierSideBySide
	case avail >= tapeOnlyMinScene+dataLinkH && m.width >= mainframeSceneMinWidth:
		return tierTapeOnly
	default:
		return tierMinimal
	}
}

// mainframeSceneMinWidth — below this terminal width we render the
// data-link panel without the mainframe/cable/tape wrapping (insufficient
// horizontal room for the cabinet block).
const mainframeSceneMinWidth = 76

// Program is the global tea.Program; goroutines call Program.Send() to deliver messages.
var Program *tea.Program

// NewTUIModel creates a new TUI model for the given number of connections.
// batchCurrent and batchTotal are 1-based; pass 0,0 when not in batch mode.
// onSkip is non-nil only in batch mode and is invoked when the user presses
// 's'.  onQuit is invoked on 'q' / 'ctrl+c'; both default to no-ops if nil.
func NewTUIModel(numConns int, willVerify bool, batchCurrent, batchTotal int, onSkip, onQuit func()) tuiModel {
	return NewTUIModelWithHistory(numConns, willVerify, batchCurrent, batchTotal, nil, onSkip, onQuit)
}

// NewTUIModelWithHistory is the variant that lets the caller seed the
// per-batch-item lifecycle record.  Used by RunWithTUI when the parent
// batch loop has accumulated skip / fail / done history before this TUI
// session was started.
func NewTUIModelWithHistory(
	numConns int,
	willVerify bool,
	batchCurrent, batchTotal int,
	history []BatchItemSnapshot,
	onSkip, onQuit func(),
) tuiModel {
	s := spinner.New()
	s.Spinner = signalPulse
	s.Style = lipgloss.NewStyle().Foreground(colorAmber)
	return tuiModel{
		numConns:      numConns,
		maxLogs:       5,
		spinner:       s,
		width:         80,
		willVerify:    willVerify,
		batchCurrent:  batchCurrent,
		batchTotal:    batchTotal,
		batchHistory:  history,
		overallSpring: harmonica.NewSpring(harmonica.FPS(60), 7.0, 0.85),
		joinSpring:    harmonica.NewSpring(harmonica.FPS(60), 7.0, 0.85),
		onSkip:        onSkip,
		onQuit:        onQuit,
		speedHistory:  make([]float64, 0, sparklineWidth),
		link:          newDataLink(),
		modem:         newModemHandshake(),
		joinAnim:      newJoinAnimation(),
		verifyAnim:    newVerifyAnimation(),
		mf:            newMainframe(),
		bus:           newCable(),
		tape:          newTape(tapeLabelFor(batchCurrent)),
	}
}

// sanStatusFromBatch maps the batch-layer lifecycle status onto the
// SAN's bay status.  Aborted items render the same as failed because
// both terminate without producing a usable file.
func sanStatusFromBatch(s BatchItemStatus) sanItemStatus {
	switch s {
	case BatchItemDone:
		return sanDone
	case BatchItemSkipped:
		return sanSkipped
	case BatchItemFailed:
		return sanFailed
	case BatchItemAborted:
		return sanFailed
	}
	return sanQueued
}

// tapeLabelFor produces the plate text on the tape unit.  In batch mode
// the active item's index becomes the label; otherwise a generic name.
func tapeLabelFor(batchCurrent int) string {
	if batchCurrent > 0 {
		return fmt.Sprintf("TAPE-%02d", batchCurrent)
	}
	return "TAPE-01"
}

// Sparkline configuration — a rolling history of total download speed
// rendered with Unicode block-shading characters for sub-line resolution.
const sparklineWidth = 48

var sparklineRunes = []rune{' ', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

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
		m.height = msg.Height
		bw := calcBarWidth(m.width)
		for i := range m.parts {
			m.parts[i].bar = newPartBar(bw)
		}
		if m.started {
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

		// Overall spring.  Prefer the server-reported total; fall back to
		// the sum of per-part totals when Content-Length is missing
		// ("size: unknown" downloads still know each part's individual
		// total, so the aggregate is recoverable).  Without the fallback
		// the active tape and aggregate bar would be pinned at 0%.
		overallTarget := 0.0
		switch {
		case m.size > 0:
			overallTarget = math.Min(float64(m.totalDown)/float64(m.size), 1.0)
		default:
			var sumDown, sumTotal int64
			allDone := len(m.parts) > 0
			for _, p := range m.parts {
				sumDown += p.downloaded
				sumTotal += p.total
				if !p.done {
					allDone = false
				}
			}
			switch {
			case allDone:
				overallTarget = 1.0
			case sumTotal > 0:
				overallTarget = math.Min(float64(sumDown)/float64(sumTotal), 1.0)
			}
		}
		m.overallPct, m.overallVel = m.overallSpring.Update(m.overallPct, m.overallVel, overallTarget)
		if m.overallPct < 0 {
			m.overallPct = 0
		}

		// Sample the total speed for the sparkline.  We sample on every tick
		// (16ms) but downsample to ~6 Hz so the sparkline reads as a smooth
		// recent-history graph rather than per-frame jitter.
		var totalSpeed float64
		for _, p := range m.parts {
			totalSpeed += p.speed
		}
		if totalSpeed > m.peakSpeed {
			m.peakSpeed = totalSpeed
		}

		// Drive the data-link LED animation from current per-channel rates.
		partSpeeds := make([]float64, len(m.parts))
		for i, p := range m.parts {
			partSpeeds[i] = p.speed
		}
		m.link.Tick(totalSpeed, m.peakSpeed, partSpeeds)

		// Drive mainframe / cable / tape animations.  Their state is
		// derived from the lifecycle flags (stopping/skipping/done/etc.)
		// and the rate-ratio drives packet velocity on the bus and reel
		// rotation on the tape.
		rateRatio := 0.0
		if m.peakSpeed > 0 {
			rateRatio = math.Min(totalSpeed/m.peakSpeed, 1.0)
		}
		mfState, busState, tpState := m.deriveSceneStates()
		m.mf.SetState(mfState)
		m.bus.SetState(busState)
		m.tape.SetState(tpState)
		// During assembly the tape fill-bar tracks joinPct so the strip
		// visually rewinds from full to 0 and back to full as parts
		// merge, matching the LINK aggregate bar.
		tapePct := m.overallPct
		if m.joining {
			tapePct = math.Min(m.joinPct, 1.0)
		}
		m.tape.Update(tapePct, totalSpeed, m.peakSpeed)
		m.mf.Tick()
		m.bus.Tick(rateRatio)
		m.tape.Tick()

		// Batch SAN: lazily build it once we know we're in a batch and
		// keep it in sync with per-item state.  Only the active item's
		// progress is live (each batch item runs its own TUI session).
		if m.batchTotal > 1 {
			if !m.hasSan {
				items := make([]sanItem, m.batchTotal)
				for i := range items {
					items[i] = sanItem{
						// Compact label fits in the bay plate.
						Label:  fmt.Sprintf("T-%02d", i+1),
						Status: sanQueued,
					}
				}
				// Seed prior bays from the parent batch loop's history
				// when available — tells DONE / SKIPPED / FAILED apart.
				// Without history, fall back to "everything before
				// current is DONE" (legacy behaviour).
				if len(m.batchHistory) == m.batchTotal {
					for i, h := range m.batchHistory {
						if h.Label != "" {
							items[i].Label = h.Label
						}
						items[i].Status = sanStatusFromBatch(h.Status)
					}
				} else {
					for i := 0; i < m.batchCurrent-1; i++ {
						items[i].Status = sanDone
					}
				}
				if m.batchCurrent >= 1 && m.batchCurrent <= len(items) {
					items[m.batchCurrent-1].Status = sanActive
				}
				m.sanView = newSan(items)
				m.sanView.SetActive(m.batchCurrent - 1)
				m.hasSan = true
			}
			m.sanView.SetWidth(m.width)
			// Budget the SAN's height so it window-clips bay rows that
			// don't fit (terminal-aware layout — never overruns the
			// bottom of the screen).
			chrome := chromeMinHeight + 2 // batch counter banner adds 2
			dataLinkH := len(m.parts) + 8
			if dataLinkH < 12 {
				dataLinkH = 12
			}
			sanBudget := m.height - chrome - dataLinkH
			if sanBudget < 0 {
				sanBudget = 0
			}
			m.sanView.SetHeight(sanBudget)
			m.sanView.Update(m.overallPct, totalSpeed, m.peakSpeed, mfState, busState)
			m.sanView.Tick(rateRatio)
			// Replace the active bay's generic label with the actual
			// filename (truncated to fit the bay plate).  Other bays
			// keep their T-NN slot identifiers — once we move on to the
			// next item, this gets overwritten with the new file.
			if m.fileName != "" && m.batchCurrent >= 1 && m.batchCurrent <= len(m.sanView.items) {
				m.sanView.items[m.batchCurrent-1].Label = m.fileName
			}
			// Reflect current-item state on the active tape.
			if m.stopping || m.skipping {
				if m.batchCurrent >= 1 && m.batchCurrent <= len(m.sanView.items) {
					switch {
					case m.skipping:
						m.sanView.items[m.batchCurrent-1].Status = sanSkipped
					case m.stopping:
						m.sanView.items[m.batchCurrent-1].Status = sanFailed
					}
				}
			}
		}

		// Advance modem handshake animation when not started
		if !m.started {
			m.modem.SetWidth(m.width)
			m.modem.Tick()
		}

		// Advance join animation when joining
		if m.joining {
			m.joinAnim.Tick()
		}

		// Advance verify animation while the vault panel is on screen —
		// either during scanning (verifying=true) or after the result
		// is known but the closing screen is still visible (verifyDone
		// + willVerify), so the LEDs and rivet rows keep their pulse.
		if m.verifying || (m.verifyDone && m.willVerify) {
			m.verifyAnim.Tick()
		}

		if len(m.speedHistory) == 0 || time.Since(m.startTime).Milliseconds()%160 < 20 {
			m.speedHistory = append(m.speedHistory, totalSpeed)
			if len(m.speedHistory) > sparklineWidth {
				m.speedHistory = m.speedHistory[len(m.speedHistory)-sparklineWidth:]
			}
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
		// Drive the vault panel into its terminal state with parsed
		// signing details — replaces the bespoke "verifying…" spinner
		// with a structured fingerprint / signed-by readout.
		details := ParseGPGOutput(msg.Detail)
		if msg.OK {
			m.verifyAnim.SetVerified(details)
		} else {
			m.verifyAnim.SetBreached(details)
		}
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
		if m.batchMode {
			return m, nil
		}
		return m, autoQuitCmd()

	case DownloadErrorMsg:
		isSkip := m.skipping ||
			(msg.Err != nil && msg.Err.Error() == "skip current item")
		if isSkip {
			m.skipping = true // make the scene primitives stay disconnected
			m.skipped = true
			if m.batchMode {
				return m, nil
			}
			return m, autoQuitCmd()
		}
		m.hasError = true
		if msg.Err != nil {
			m.errMsg = msg.Err.Error()
		} else {
			m.errMsg = "unknown error"
		}
		if m.batchMode {
			return m, nil
		}
		return m, autoQuitCmd()

	case BatchItemBeginMsg:
		// Reset per-item state — but keep the SAN, history, dimensions,
		// and batchMode intact.  This is the seam between items inside a
		// single persistent tea.Program.
		m.url = msg.URL
		m.fileName = msg.FileName
		m.size = 0
		m.ips = nil
		m.parts = nil
		m.totalDown = 0
		m.peakSpeed = 0
		m.speedHistory = m.speedHistory[:0]
		// Start the elapsed clock now so the data-link panel's stats
		// row reads sensibly during the inter-item handshake before
		// DownloadStartMsg arrives.  DownloadStartMsg will overwrite it.
		m.startTime = time.Now()
		m.overallPct = 0
		m.overallVel = 0
		m.joinPct = 0
		m.joinVel = 0
		m.started = false
		m.done = false
		m.hasError = false
		m.errMsg = ""
		m.skipping = false
		m.skipped = false
		m.stopping = false
		m.stoppingReason = ""
		m.joining = false
		m.joinCurrent = 0
		m.joinTotal = 0
		m.verifying = false
		m.verifyDone = false
		m.verifyOK = false
		m.verifyDetail = ""
		m.logs = m.logs[:0]
		m.batchCurrent = msg.Index + 1
		// Re-init animations so each item starts with a fresh handshake.
		m.modem = newModemHandshake()
		m.link = newDataLink()
		m.tape = newTape(tapeLabelFor(m.batchCurrent))
		if m.hasSan && msg.Index >= 0 && msg.Index < len(m.sanView.items) {
			if msg.FileName != "" {
				m.sanView.items[msg.Index].Label = msg.FileName
			}
			m.sanView.items[msg.Index].Status = sanActive
			m.sanView.SetActive(msg.Index)
		}
		return m, nil

	case BatchItemEndMsg:
		if m.hasSan && msg.Index >= 0 && msg.Index < len(m.sanView.items) {
			m.sanView.items[msg.Index].Status = sanStatusFromBatch(msg.Status)
		}
		return m, nil

	case BatchFinishedMsg:
		m.batchFinished = true
		m.batchFinalDone = msg.Done
		m.batchFinalFailed = msg.Failed
		m.batchFinalSkipped = msg.Skipped
		m.batchFinalAborted = msg.Aborted
		m.done = true
		return m, autoQuitCmd()

	case autoQuitMsg:
		return m, tea.Quit
	}
	return m, nil
}

// ── Bar constructors ──────────────────────────────────────────────────────────

func newPartBar(w int) progress.Model {
	// Per-part: fades from deep cyan to phosphor — single-hue, refined.
	return progress.New(
		progress.WithGradient("#1E7A99", "#73E0FF"),
		progress.WithWidth(w),
		progress.WithoutPercentage(),
	)
}

func newJoinBar(w int) progress.Model {
	// Join: amber → mint, signalling "almost finished".
	return progress.New(
		progress.WithGradient("#FFB75A", "#5EE6A1"),
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
	// Soft body separator using a fine dotted glyph instead of the heavy
	// horizontal line, plus an accent rule under the banner.
	sep := styleSep.Render(strings.Repeat("┄", sepWidth(w)))
	accentRule := lipgloss.NewStyle().Foreground(colorPhosphor).Render(strings.Repeat("═", sepWidth(w)))

	// Header — during the active download phase, render the animated
	// data-link panel (which absorbs per-channel + aggregate readouts).
	// Otherwise fall back to the wordmark banner.
	useLink := m.started && !m.done && !m.hasError && w >= dataLinkInnerW+4
	if !useLink {
		b.WriteString(styleBanner.Render(banner))
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Foreground(colorSteel).Render(bannerStrap))
		b.WriteString("\n")
		b.WriteString(accentRule)
		b.WriteString("\n\n")
	}

	// Batch progress indicator.
	if m.batchTotal > 1 {
		doneSoFar := m.batchCurrent - 1
		remaining := m.batchTotal - m.batchCurrent
		idx := lipgloss.NewStyle().Foreground(colorAmber).Bold(true).
			Render(fmt.Sprintf("%02d", m.batchCurrent))
		of := lipgloss.NewStyle().Foreground(colorSteel).
			Render(fmt.Sprintf(" / %02d", m.batchTotal))
		batchLine := "  ⌘  batch " + idx + of
		if doneSoFar > 0 {
			batchLine += "  " + styleSep.Render("│") + "  " + styleDone.Render(fmt.Sprintf("%d done", doneSoFar))
		}
		if remaining > 0 {
			batchLine += "  " + styleSep.Render("│") + "  " + lipgloss.NewStyle().Foreground(colorPhosphor).Render(fmt.Sprintf("%d queued", remaining))
		}
		b.WriteString(lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(colorAmber).
			Padding(0, 0, 0, 1).
			Foreground(colorFrost).
			Render(batchLine) + "\n\n")
	}

	// Batch summary screen — final frame shown after every item has run.
	if m.batchFinished {
		return m.renderBatchSummary(sep)
	}

	// Pre-start spinner — used only for the very first frame of a
	// single-item session.  In batch mode we never take this branch
	// (even before the first DownloadStartMsg arrives) so the data-link
	// scene stays mounted across the whole batch — the inner stage
	// just transitions from HANDSHAKE → DOWNLOADING → ASSEMBLING as
	// each item runs, without ever replacing the panel itself.
	if !m.started && !m.batchMode && !m.hasError {
		modemView := m.modem.View(m.url)
		for _, line := range strings.Split(modemView, "\n") {
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")

		if m.stopping || m.skipping {
			b.WriteString("\n" + m.renderStopOverlay() + "\n")
		}

		b.WriteString(sep + "\n")
		b.WriteString(m.renderFooter())
		return b.String()
	}

	if m.skipped && !m.batchMode {
		return m.renderSkipScreen(sep)
	}

	// Error screen.
	if m.hasError && !m.batchMode {
		// Build channel state at error point
		channels := make([]channelRow, len(m.parts))
		for i, p := range m.parts {
			pct := 0.0
			if p.total > 0 {
				pct = float64(p.downloaded) / float64(p.total)
			}
			channels[i] = channelRow{
				Index:      i + 1,
				Pct:        pct,
				RawPct:     pct,
				Speed:      0,
				Done:       false,
				HasStarted: p.downloaded > 0,
			}
		}

		linkView := m.link.View(channels, m.totalDown, m.size, m.peakSpeed,
			time.Since(m.startTime), "ERROR", false)
		// Centre the panel
		pad := (w - dataLinkInnerW - 2) / 2
		if pad < 0 {
			pad = 0
		}
		padStr := strings.Repeat(" ", pad)
		for _, line := range strings.Split(linkView, "\n") {
			b.WriteString(padStr + line + "\n")
		}

		b.WriteString("\n")
		b.WriteString("  " + styleError.Render("◆ LINK FAILED") + "\n\n")
		b.WriteString(styleErrBox.MarginLeft(2).Render(m.errMsg) + "\n\n")
		b.WriteString(sep + "\n")
		b.WriteString(styleHelp.Render("  closing in 3 s  •  ") + styleHelpKey.Render("q") + styleHelp.Render(" quit now"))
		return b.String()
	}

	// Metadata row.  Slim, refined typographic hierarchy:
	// dim label · accent value · monospace muted secondary.
	urlDisplay := m.url
	if maxU := w - 14; len(urlDisplay) > maxU && maxU > 4 {
		urlDisplay = urlDisplay[:maxU-1] + "…"
	}
	tri := lipgloss.NewStyle().Foreground(colorAmber).Render("▸")
	b.WriteString("  " + tri + " " + styleLabel.Render("url") + " " + styleValue.Render(urlDisplay) + "\n")
	b.WriteString("    " + styleLabel.Render("file") + " " + styleAccentValue.Render(m.fileName) + "\n")
	b.WriteString("    " + styleLabel.Render("size") + " " + styleAccentValue.Render(formatBytes(m.size)) + "\n")
	b.WriteString("    " + styleLabel.Render("conns") + " " + styleAccentValue.Render(fmt.Sprintf("%d", m.numConns)) + "\n")
	if len(m.ips) > 0 {
		b.WriteString("    " + styleLabel.Render("ips") + " " + styleValue.Render(strings.Join(m.ips, " · ")) + "\n")
	}
	b.WriteString("\n" + sep + "\n\n")

	// Completion screen.
	if m.done && !m.joining && !m.batchMode {
		// Build final channel state (all at 100%)
		channels := make([]channelRow, len(m.parts))
		for i := range m.parts {
			channels[i] = channelRow{
				Index:      i + 1,
				Pct:        1.0,
				RawPct:     1.0,
				Speed:      0,
				Done:       true,
				HasStarted: true,
			}
		}

		linkView := m.link.View(channels, m.size, m.size, m.peakSpeed,
			time.Since(m.startTime), "COMPLETE", true)
		// Centre the panel
		pad := (w - dataLinkInnerW - 2) / 2
		if pad < 0 {
			pad = 0
		}
		padStr := strings.Repeat(" ", pad)
		for _, line := range strings.Split(linkView, "\n") {
			b.WriteString(padStr + line + "\n")
		}

		b.WriteString("\n")

		elapsed := time.Since(m.startTime)
		var avg float64
		if s := elapsed.Seconds(); s > 0 {
			avg = float64(m.size) / s
		}
		b.WriteString("  " + styleDone.Render("◆ LINK CLOSED") + "\n\n")
		b.WriteString("    " + styleLabel.Render("time") + " " + styleAccentValue.Render(formatDuration(elapsed)) + "\n")
		b.WriteString("    " + styleLabel.Render("avg") + " " + styleAccentValue.Render(formatSpeed(avg)) + "\n")
		b.WriteString("    " + styleLabel.Render("peak") + " " + styleAccentValue.Render(formatSpeed(m.peakSpeed)) + "\n")
		b.WriteString("    " + styleLabel.Render("saved") + " " + styleValue.Render(m.fileName) + "\n")
		// Verification result — render the full vault panel beneath the
		// summary so success / failure carries the same chrome / LED
		// vibe as the rest of the carrier theme, with parsed fingerprint
		// + signed-by detail rows.
		if m.willVerify {
			b.WriteString("\n")
			vaultPad := (w - vaultWidth) / 2
			if vaultPad < 0 {
				vaultPad = 0
			}
			vPadStr := strings.Repeat(" ", vaultPad)
			vaultView := m.verifyAnim.View()
			for _, line := range strings.Split(vaultView, "\n") {
				b.WriteString(vPadStr + line + "\n")
			}
		}
		b.WriteString("\n" + sep + "\n")
		b.WriteString(styleHelp.Render("  closing in 3 s  •  ") + styleHelpKey.Render("q") + styleHelp.Render(" quit now"))
		return b.String()
	}

	// Verifying phase (download+join complete, waiting for GPG result).
	// In single-item mode we replace the scene with the full vault panel.
	// In batch mode we keep the data-link scene mounted (status flips to
	// VERIFYING, with an inline indicator below the panel) so there is
	// no jumpscare between items.
	if m.verifying && !m.done && !m.batchMode {
		vaultPad := (w - vaultWidth) / 2
		if vaultPad < 0 {
			vaultPad = 0
		}
		vPadStr := strings.Repeat(" ", vaultPad)
		verifyView := m.verifyAnim.View()
		for _, line := range strings.Split(verifyView, "\n") {
			b.WriteString(vPadStr + line + "\n")
		}
		b.WriteString("\n")

		if m.stopping || m.skipping {
			b.WriteString("\n" + m.renderStopOverlay() + "\n")
		}

		b.WriteString(sep + "\n")
		b.WriteString(m.renderFooter())
		return b.String()
	}

	// Build per-channel rows for the data-link panel.  During the join
	// phase the channels are "absorbed" into the aggregate — they keep
	// their slot but render as DONE so the layout stays identical.
	//
	// In batch mode, between items, m.parts may be empty (the next
	// item's range probe hasn't returned yet).  We synthesize
	// placeholder rows from m.numConns so the panel's height never
	// collapses — only the LEDs and bars animate.
	partCount := len(m.parts)
	placeholders := partCount == 0 && m.batchMode && m.numConns > 0
	if placeholders {
		partCount = m.numConns
	}
	channels := make([]channelRow, partCount)
	for i := 0; i < partCount; i++ {
		if placeholders {
			channels[i] = channelRow{
				Index:      i,
				Pct:        0,
				RawPct:     0,
				Speed:      0,
				Done:       false,
				HasStarted: false,
			}
			continue
		}
		p := m.parts[i]
		rawPct := 0.0
		if p.total > 0 {
			rawPct = math.Min(float64(p.downloaded)/float64(p.total), 1.0)
		}
		if p.done {
			rawPct = 1.0
		}
		done := p.done
		smoothPct := p.smoothPct
		if m.joining {
			// During assembly, channels gradually flip "absorbed" left-to-right
			// as merging proceeds.  Visually shows parts being consumed into
			// the output stream.
			done = true
			rawPct = 1.0
			smoothPct = 1.0
		}
		channels[i] = channelRow{
			Index:      i,
			Pct:        math.Max(0, math.Min(smoothPct, 1.0)),
			RawPct:     rawPct,
			Speed:      p.speed,
			Done:       done,
			HasStarted: p.downloaded > 0,
		}
	}

	// Compose link status word from lifecycle state.
	status := "DOWNLOADING"
	online := true
	switch {
	case m.stopping:
		status = "STOPPING"
		online = false
	case m.skipping:
		status = "SKIPPING"
		online = false
	case m.verifying:
		// Verification only enters the data-link scene path in batch
		// mode (single-item mode short-circuits above).
		status = "VERIFYING"
	case m.joining:
		status = "ASSEMBLING"
	case placeholders, !m.started, m.totalDown == 0:
		status = "HANDSHAKE"
		online = false
	default:
		status = "DOWNLOADING"
	}

	// During assembly, override the LINK aggregate bar to track joinPct
	// so the bar replays from 0→100% as merging proceeds.  Channels are
	// already at 100% so the previous totalDown==size would pin it full.
	linkTotal := m.totalDown
	linkSize := m.size
	if m.joining {
		// Use a synthetic size pair so the aggregate bar reflects joinPct.
		linkSize = int64(len(m.parts))
		if linkSize < 1 {
			linkSize = 1
		}
		linkTotal = int64(math.Round(math.Min(m.joinPct, 1.0) * float64(linkSize)))
	}

	linkView := m.link.View(channels, linkTotal, linkSize, m.peakSpeed,
		time.Since(m.startTime), status, online)
	// Centre the panel in the terminal.
	pad := (w - dataLinkInnerW - 2) / 2
	if pad < 0 {
		pad = 0
	}
	padStr := strings.Repeat(" ", pad)

	tier := m.computeTier()
	switch {
	case m.batchTotal > 1 && m.hasSan && tier >= tierTapeOnly:
		m.sanView.SetCompact(tier < tierFull)
		for _, line := range strings.Split(m.sanView.View(), "\n") {
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	case tier > tierMinimal:
		b.WriteString(m.renderMainframeScene())
	}

	for _, line := range strings.Split(linkView, "\n") {
		b.WriteString(padStr + line + "\n")
	}

	// Below the data-link panel: a single status line whose content
	// reflects the current phase.  Keeping the line present (rather
	// than appearing/disappearing) prevents vertical layout shifts
	// between phases — only the contents animate.
	spinners := []string{"◐", "◓", "◑", "◒"}
	dimStyle := lipgloss.NewStyle().Foreground(colorSteel)
	switch {
	case m.joining:
		mergeStyle := lipgloss.NewStyle().Foreground(colorAmber).Bold(true)
		spin := mergeStyle.Render(spinners[m.joinAnim.frame%len(spinners)])
		cur := m.joinCurrent
		if cur < 1 {
			cur = 1
		}
		if cur > m.joinTotal {
			cur = m.joinTotal
		}
		line := "  " + spin + "  " +
			mergeStyle.Render("ASSEMBLING") + "  " +
			dimStyle.Render(fmt.Sprintf("part %d of %d  ·  %.1f%%",
				cur, m.joinTotal, math.Min(m.joinPct, 1.0)*100))
		b.WriteString("\n" + padStr + line + "\n")
	case m.verifying && m.batchMode:
		vStyle := lipgloss.NewStyle().Foreground(colorPhosphor).Bold(true)
		spin := vStyle.Render(spinners[(m.verifyAnim.Frame())%len(spinners)])
		line := "  " + spin + "  " +
			vStyle.Render("VERIFYING") + "  " +
			dimStyle.Render("checking signature")
		b.WriteString("\n" + padStr + line + "\n")
	case placeholders || (!m.started && m.batchMode):
		// Inter-item handshake — the next item's range probe is in
		// flight.  A muted "preparing next transfer" placeholder keeps
		// the row populated so the panel doesn't appear to flicker.
		hStyle := lipgloss.NewStyle().Foreground(colorPhosphor)
		spin := hStyle.Render(spinners[(m.modem.Frame())%len(spinners)])
		label := "preparing next transfer"
		if m.fileName != "" {
			label = "preparing  " + m.fileName
		}
		line := "  " + spin + "  " +
			hStyle.Render("HANDSHAKE") + "  " +
			dimStyle.Render(label)
		b.WriteString("\n" + padStr + line + "\n")
	default:
		if spark := m.renderSparkline(); spark != "" {
			peakLabel := lipgloss.NewStyle().Foreground(colorSteel).Render("peak " + formatBytes(int64(m.peakSpeed)) + "/s")
			rateLabel := lipgloss.NewStyle().Foreground(colorSteel).Render("rate ↗ ")
			b.WriteString("\n" + padStr + "  " + rateLabel + spark + "  " + peakLabel + "\n")
		}
	}

	// Log panel.
	if len(m.logs) > 0 {
		b.WriteString("\n  " + styleSectionChip.Render("EVENTS") +
			styleSep.Render(strings.Repeat("┄", maxInt(0, sepWidth(w)-11))) + "\n")
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

// deriveSceneStates maps the model's lifecycle flags onto the mainframe /
// cable / tape state machines used by the 90s scene primitives.
func (m tuiModel) deriveSceneStates() (mainframeState, cableState, tapeState) {
	switch {
	case m.hasError:
		return mfAlarm, cableDisconnected, tapeDisconnected
	case m.skipping:
		return mfAlarm, cableDisconnected, tapeDisconnected
	case m.stopping:
		return mfAlarm, cableDisconnected, tapeDisconnected
	case m.done && !m.joining:
		return mfComplete, cableComplete, tapeComplete
	case m.joining:
		return mfTransferring, cableActive, tapeTransferring
	case !m.started:
		return mfIdle, cableIdle, tapeIdle
	case m.totalDown == 0:
		return mfHandshaking, cableConnecting, tapeMounting
	default:
		return mfTransferring, cableActive, tapeTransferring
	}
}

// renderMainframeScene composes the mainframe scene appropriate for the
// current layout tier.  Returns empty string for tierMinimal.
func (m tuiModel) renderMainframeScene() string {
	switch m.computeTier() {
	case tierFull:
		return m.renderSceneFullStack()
	case tierSideBySide:
		return m.renderSceneSideBySide()
	case tierTapeOnly:
		return m.renderSceneTapeOnly()
	default:
		return ""
	}
}

// renderSceneFullStack — vertical stack: mainframe → cable → tape banner.
func (m tuiModel) renderSceneFullStack() string {
	w := m.width
	if w < mainframeSceneMinWidth {
		return ""
	}

	mfPad := (w - mainframeWidth) / 2
	if mfPad < 0 {
		mfPad = 0
	}
	mfPadStr := strings.Repeat(" ", mfPad)
	mfLines := strings.Split(m.mf.View(), "\n")

	var b strings.Builder
	for _, line := range mfLines {
		b.WriteString(mfPadStr + line + "\n")
	}

	const cableRows = 3
	portCol := m.mf.PortColumn()
	cableLines := strings.Split(m.bus.View(cableRows, mainframeWidth, portCol), "\n")
	for _, line := range cableLines {
		b.WriteString(mfPadStr + line + "\n")
	}

	bannerPad := (w - tapeBannerWidth) / 2
	if bannerPad < 0 {
		bannerPad = 0
	}
	bannerPadStr := strings.Repeat(" ", bannerPad)
	for _, line := range strings.Split(m.tape.ViewBanner(), "\n") {
		b.WriteString(bannerPadStr + line + "\n")
	}
	return b.String()
}

// renderSceneSideBySide — mainframe LEFT, horizontal cable bridge MIDDLE,
// tape banner RIGHT (vertically centered against the taller mainframe).
// Total width: mainframeWidth + 1 + bridgeWidth + 1 + tapeBannerWidth.
func (m tuiModel) renderSceneSideBySide() string {
	const bridgeW = 11

	mfLines := strings.Split(m.mf.View(), "\n")
	tapeLines := strings.Split(m.tape.ViewBanner(), "\n")

	mfH := len(mfLines)
	tapeH := len(tapeLines)
	tapeTopPad := (mfH - tapeH) / 2
	if tapeTopPad < 0 {
		tapeTopPad = 0
	}

	// Pad tape vertically to mfH rows so we can stitch row-by-row.
	padded := make([]string, mfH)
	blank := strings.Repeat(" ", tapeBannerWidth)
	for i := 0; i < mfH; i++ {
		padded[i] = blank
	}
	for i := 0; i < tapeH && tapeTopPad+i < mfH; i++ {
		padded[tapeTopPad+i] = tapeLines[i]
	}

	// Horizontal cable bridge.  Drawn at the vertical centre row,
	// matching the tape's recording head row so it visually connects to
	// the magnetic strip.
	bridgeRow := tapeTopPad + tapeH/2
	bridge := m.renderBridge(bridgeW, mfH, bridgeRow)

	// Total scene width.
	sceneW := mainframeWidth + 1 + bridgeW + 1 + tapeBannerWidth
	scenePad := (m.width - sceneW) / 2
	if scenePad < 0 {
		scenePad = 0
	}
	padStr := strings.Repeat(" ", scenePad)

	var b strings.Builder
	for i := 0; i < mfH; i++ {
		b.WriteString(padStr + mfLines[i] + " " + bridge[i] + " " + padded[i] + "\n")
	}
	return b.String()
}

// renderBridge produces an mfH-row × width-cell horizontal cable that
// connects the mainframe (left) to the tape (right) at bridgeRow.
func (m tuiModel) renderBridge(width, rows, bridgeRow int) []string {
	chrome := lipgloss.NewStyle().Foreground(colorPhosphor)
	mint := lipgloss.NewStyle().Foreground(colorMint).Bold(true)
	mag := lipgloss.NewStyle().Foreground(colorMagenta).Bold(true)
	amber := lipgloss.NewStyle().Foreground(colorAmber).Bold(true)
	dim := lipgloss.NewStyle().Foreground(colorSlate)

	// Pick palette mirroring the cable's state.
	mfState, busState, _ := m.deriveSceneStates()
	_ = mfState
	var lineSty lipgloss.Style
	switch busState {
	case cableComplete:
		lineSty = mint
	case cableDisconnected:
		lineSty = mag
	case cableConnecting:
		lineSty = amber
	case cableIdle:
		lineSty = dim
	default:
		lineSty = chrome
	}

	out := make([]string, rows)
	blank := strings.Repeat(" ", width)
	for i := 0; i < rows; i++ {
		out[i] = blank
	}

	// Animated packet position along the bridge.
	pkt := -1
	if busState == cableActive || busState == cableConnecting {
		pkt = (m.tape.frame / 2) % width
	}

	if bridgeRow < 0 {
		bridgeRow = rows / 2
	}
	if bridgeRow >= rows {
		bridgeRow = rows - 1
	}

	// Build the line with connector caps at each end.
	var line strings.Builder
	for c := 0; c < width; c++ {
		switch {
		case c == 0 || c == width-1:
			line.WriteString(lineSty.Render("▣"))
		case busState == cableDisconnected && c%3 == 1:
			line.WriteByte(' ')
		case c == pkt:
			switch busState {
			case cableComplete:
				line.WriteString(mint.Render("●"))
			case cableConnecting:
				line.WriteString(amber.Render("●"))
			default:
				line.WriteString(amber.Render("●"))
			}
		case busState == cableDisconnected:
			line.WriteString(lineSty.Render("╴"))
		default:
			line.WriteString(lineSty.Render("═"))
		}
	}
	out[bridgeRow] = line.String()

	// Add support brackets above/below the bridge for a "patch panel" feel.
	if bridgeRow-1 >= 0 {
		var sup strings.Builder
		for c := 0; c < width; c++ {
			switch {
			case c == 0 || c == width-1:
				sup.WriteString(dim.Render("│"))
			default:
				sup.WriteByte(' ')
			}
		}
		out[bridgeRow-1] = sup.String()
	}
	if bridgeRow+1 < rows {
		var sup strings.Builder
		for c := 0; c < width; c++ {
			switch {
			case c == 0 || c == width-1:
				sup.WriteString(dim.Render("│"))
			default:
				sup.WriteByte(' ')
			}
		}
		out[bridgeRow+1] = sup.String()
	}

	return out
}

// renderSceneTapeOnly — tape banner alone.  Used when there's enough room
// for the tape decoration but not the full mainframe stack.
func (m tuiModel) renderSceneTapeOnly() string {
	w := m.width
	bannerPad := (w - tapeBannerWidth) / 2
	if bannerPad < 0 {
		bannerPad = 0
	}
	padStr := strings.Repeat(" ", bannerPad)
	var b strings.Builder
	for _, line := range strings.Split(m.tape.ViewBanner(), "\n") {
		b.WriteString(padStr + line + "\n")
	}
	return b.String()
}

// renderSparkline turns the rolling speed history into a left-padded sparkline
// of constant width.  The bars are coloured phosphor cyan, with the most-recent
// (rightmost) bar amber to highlight current activity.
func (m tuiModel) renderSparkline() string {
	if len(m.speedHistory) == 0 || m.peakSpeed == 0 {
		return ""
	}
	max := m.peakSpeed
	out := make([]rune, 0, sparklineWidth)
	// Pad with empty cells when history is shorter than the spark width.
	pad := sparklineWidth - len(m.speedHistory)
	for i := 0; i < pad; i++ {
		out = append(out, ' ')
	}
	for _, v := range m.speedHistory {
		idx := int(v / max * float64(len(sparklineRunes)-1))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(sparklineRunes) {
			idx = len(sparklineRunes) - 1
		}
		out = append(out, sparklineRunes[idx])
	}
	body := lipgloss.NewStyle().Foreground(colorPhosphor).Render(string(out[:len(out)-1]))
	tip := lipgloss.NewStyle().Foreground(colorAmber).Bold(true).Render(string(out[len(out)-1]))
	return body + tip
}

func (m tuiModel) renderSkipScreen(sep string) string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}

	// Banner header to anchor the screen — same as on the pre-start view.
	b.WriteString(styleBanner.Render(banner) + "\n")
	b.WriteString(lipgloss.NewStyle().Foreground(colorSteel).Render(bannerStrap) + "\n")
	b.WriteString(lipgloss.NewStyle().Foreground(colorPhosphor).
		Render(strings.Repeat("═", sepWidth(w))) + "\n\n")

	// Optional batch counter (mirrors the live-download view).
	if m.batchTotal > 1 {
		idx := lipgloss.NewStyle().Foreground(colorAmber).Bold(true).
			Render(fmt.Sprintf("%02d", m.batchCurrent))
		of := lipgloss.NewStyle().Foreground(colorSteel).
			Render(fmt.Sprintf(" / %02d", m.batchTotal))
		batchLine := "  ⌘  batch " + idx + of +
			"  " + styleSep.Render("│") + "  " +
			lipgloss.NewStyle().Foreground(colorAmber).Bold(true).Render("⤳ skipped")
		b.WriteString(lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(colorAmber).
			Padding(0, 0, 0, 1).
			Foreground(colorFrost).
			Render(batchLine) + "\n\n")
	}

	// Render the scene with all primitives in disconnected/alarm state.
	// deriveSceneStates already maps m.skipping → mfAlarm/cableDisconnected
	// /tapeDisconnected, so we just call the regular scene render path.
	tier := m.computeTier()
	switch {
	case m.batchTotal > 1 && m.hasSan && tier >= tierTapeOnly:
		m.sanView.SetCompact(tier < tierFull)
		for _, line := range strings.Split(m.sanView.View(), "\n") {
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	case tier > tierMinimal:
		b.WriteString(m.renderMainframeScene())
	}

	// Caption block: "⤳ TRANSFER SKIPPED" header, file, next item.
	headSty := lipgloss.NewStyle().Foreground(colorAmber).Bold(true)
	b.WriteString("\n  " + headSty.Render("⤳ TRANSFER SKIPPED") + "\n\n")
	if m.fileName != "" {
		b.WriteString("    " + styleLabel.Render("file") + " " +
			styleValue.Render(m.fileName) + "\n")
	}
	if m.batchTotal > 1 && m.batchCurrent < m.batchTotal {
		b.WriteString("    " + styleLabel.Render("next") + " " +
			styleAccentValue.Render(fmt.Sprintf("item %02d / %02d",
				m.batchCurrent+1, m.batchTotal)) + "\n")
	} else if m.batchTotal > 1 {
		b.WriteString("    " + styleLabel.Render("next") + " " +
			lipgloss.NewStyle().Foreground(colorSteel).Render("(end of batch)") + "\n")
	}

	b.WriteString("\n" + sep + "\n")
	b.WriteString(styleHelp.Render("  closing in 3 s  •  ") +
		styleHelpKey.Render("q") + styleHelp.Render(" quit now"))
	return b.String()
}

// renderBatchSummary renders the final, single-frame summary that closes
// out a batch run inside the persistent TUI program.  The SAN cabinet
// stays on screen so the user sees the full lifecycle at a glance, with a
// tally row underneath.
func (m tuiModel) renderBatchSummary(sep string) string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}

	// Banner header so the closing frame matches the opening aesthetic.
	b.WriteString(styleBanner.Render(banner) + "\n")
	b.WriteString(lipgloss.NewStyle().Foreground(colorSteel).Render(bannerStrap) + "\n")
	b.WriteString(lipgloss.NewStyle().Foreground(colorPhosphor).
		Render(strings.Repeat("═", sepWidth(w))) + "\n\n")

	// SAN cabinet — full lifecycle for every bay.
	if m.hasSan {
		tier := m.computeTier()
		m.sanView.SetCompact(tier < tierFull)
		for _, line := range strings.Split(m.sanView.View(), "\n") {
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	// Tally row.
	total := len(m.sanView.items)
	if total == 0 {
		total = m.batchFinalDone + m.batchFinalFailed + m.batchFinalSkipped + m.batchFinalAborted
	}
	tally := func(label string, n int, sty lipgloss.Style) string {
		if n == 0 {
			return ""
		}
		return sty.Render(fmt.Sprintf("%d %s", n, label))
	}
	parts := []string{}
	mintB := lipgloss.NewStyle().Foreground(colorMint).Bold(true)
	amberB := lipgloss.NewStyle().Foreground(colorAmber).Bold(true)
	magB := lipgloss.NewStyle().Foreground(colorMagenta).Bold(true)
	if s := tally("done", m.batchFinalDone, mintB); s != "" {
		parts = append(parts, s)
	}
	if s := tally("skipped", m.batchFinalSkipped, amberB); s != "" {
		parts = append(parts, s)
	}
	if s := tally("failed", m.batchFinalFailed, magB); s != "" {
		parts = append(parts, s)
	}
	if s := tally("aborted", m.batchFinalAborted, amberB); s != "" {
		parts = append(parts, s)
	}
	dot := styleSep.Render("  ·  ")
	tallyLine := strings.Join(parts, dot)

	var head string
	switch {
	case m.batchFinalAborted > 0 && m.batchFinalFailed == 0:
		head = amberB.Render("⊘ BATCH ABORTED")
	case m.batchFinalFailed == 0:
		head = mintB.Render("⬢ BATCH COMPLETE")
	default:
		head = magB.Render("◈ BATCH FINISHED WITH ERRORS")
	}
	b.WriteString("  " + head + "\n")
	if tallyLine != "" {
		b.WriteString("  " + styleSep.Render("│") + "  " + tallyLine + "  " +
			styleSep.Render("│") + "  " +
			lipgloss.NewStyle().Foreground(colorSteel).Render(fmt.Sprintf("%d total", total)) + "\n")
	}

	b.WriteString("\n" + sep + "\n")
	b.WriteString(styleHelp.Render("  closing in 3 s  •  ") +
		styleHelpKey.Render("q") + styleHelp.Render(" quit now"))
	return b.String()
}

func (m tuiModel) renderStopOverlay() string {
	if m.skipping && !m.stopping {
		text := m.spinner.View() + "  Skipping current download — discarding parts"
		return styleSkipBox.MarginLeft(2).Render(text)
	}
	reason := m.stoppingReason
	if reason == "" {
		reason = "Stopping — saving state"
	}
	text := m.spinner.View() + "  " + reason
	return styleStopBox.MarginLeft(2).Render(text)
}

// keymap declares every active key binding for the TUI footer using
// bubbles/key.  The struct implements help.KeyMap so bubbles/help can
// render the bindings into a single help line.  The key.Help.Key text is
// pre-styled with the carrier keycap pill so help.View produces the same
// "rounded cap + label" aesthetic the renderer used to hand-roll.
type keymap struct {
	Skip  key.Binding
	Stop  key.Binding
	Abort key.Binding
}

// ShortHelp is bubbles/help's short-form contract.  Order = render order.
func (k keymap) ShortHelp() []key.Binding {
	bs := []key.Binding{}
	if k.Skip.Enabled() {
		bs = append(bs, k.Skip)
	}
	bs = append(bs, k.Stop, k.Abort)
	return bs
}

// FullHelp returns the same set; we don't render a separate full help.
func (k keymap) FullHelp() [][]key.Binding { return [][]key.Binding{k.ShortHelp()} }

// helpModel is a package-level help.Model with carrier-themed styles.
// Styling the ShortKey with the keycap background reproduces the original
// bespoke pill design while letting bubbles/help own the layout.
var helpModel = func() help.Model {
	h := help.New()
	keyCap := lipgloss.NewStyle().
		Foreground(colorFrost).
		Background(colorSlate).
		Bold(true).
		Padding(0, 1)
	h.Styles.ShortKey = keyCap
	h.Styles.ShortDesc = lipgloss.NewStyle().Foreground(colorMuted)
	h.Styles.ShortSeparator = lipgloss.NewStyle().Foreground(colorMuted).
		SetString("   ")
	h.Styles.FullKey = keyCap
	h.Styles.FullDesc = lipgloss.NewStyle().Foreground(colorMuted)
	h.Styles.FullSeparator = lipgloss.NewStyle().Foreground(colorMuted).
		SetString("   ")
	return h
}()

// renderFooter renders the bottom help bar via bubbles/help against the
// model's keymap.  In stopping mode we override with a single force-quit
// hint so users discover the second-press behaviour.
func (m tuiModel) renderFooter() string {
	if m.stopping {
		return "  " + styleHelp.Render("press ") +
			styleKeyCap.Render("q") +
			styleHelp.Render(" again to force-quit")
	}
	km := keymap{
		Skip: key.NewBinding(
			key.WithKeys("s", "S"),
			key.WithHelp("s", "skip item"),
			key.WithDisabled(),
		),
		Stop: key.NewBinding(
			key.WithKeys("q", "Q"),
			key.WithHelp("q", "stop & save"),
		),
		Abort: key.NewBinding(
			key.WithKeys("ctrl+c"),
			key.WithHelp("⌃C", "abort"),
		),
	}
	if m.onSkip != nil {
		km.Skip.SetEnabled(true)
	}
	return "  " + helpModel.View(km)
}

// maxInt returns the larger of a, b.  Tiny helper for sep-width math.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
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

// Stdout / Stderr — direct os handles.  termenv (used by lipgloss) detects
// the active output's capabilities and writes appropriate ANSI sequences,
// including on modern Windows terminals (10+, 2019+) which support
// VT processing natively.  go-colorable is no longer required.
var (
	Stdout    io.Writer = os.Stdout
	Stderr    io.Writer = os.Stderr
	DefaultUI           = Console{Stdout: Stdout, Stderr: Stderr}

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

type BatchItemStatus int

const (
	BatchItemQueued BatchItemStatus = iota
	BatchItemDone
	BatchItemSkipped
	BatchItemFailed
	BatchItemAborted
)

type BatchItemSnapshot struct {
	Label  string
	Status BatchItemStatus
}

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
	BatchHistory []BatchItemSnapshot
}

func RunWithTUI(opts RunOptions, fn func() error) error {
	if isatty.IsTerminal(os.Stdout.Fd()) && DisplayProgress {
		model := NewTUIModelWithHistory(
			opts.NumConns,
			opts.WillVerify,
			opts.BatchCurrent,
			opts.BatchTotal,
			opts.BatchHistory,
			opts.OnSkip,
			opts.OnQuit,
		)
		p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithoutSignalHandler())
		Program = p

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
		// Library code shouldn't terminate the process — surface the TUI
		// error to the caller (main) which decides the exit code.
		if _, err := p.Run(); err != nil {
			Program = nil
			close(stopWatch)
			<-fnDone
			if fnErr != nil {
				return fmt.Errorf("tui: %w (download error: %v)", err, fnErr)
			}
			return fmt.Errorf("tui: %w", err)
		}
		Program = nil
		close(stopWatch)
		<-fnDone
		return fnErr
	}
	// Non-TTY: run directly.
	return fn()
}

func carrierTheme() *huh.Theme {
	t := huh.ThemeBase()
	t.Focused.Title = t.Focused.Title.Foreground(colorPhosphor).Bold(true)
	t.Focused.Description = t.Focused.Description.Foreground(colorSteel)
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(colorAmber).Bold(true)
	t.Focused.UnselectedOption = t.Focused.UnselectedOption.Foreground(colorFrost)
	t.Focused.FocusedButton = t.Focused.FocusedButton.
		Foreground(colorFrost).
		Background(colorDeepCyan).
		Bold(true).
		Padding(0, 2)
	t.Focused.BlurredButton = t.Focused.BlurredButton.
		Foreground(colorSteel).
		Background(colorSlate).
		Padding(0, 2)
	t.Focused.Base = t.Focused.Base.
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorPhosphor).
		PaddingLeft(1)
	t.Blurred.Base = t.Blurred.Base.
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorSlate).
		PaddingLeft(1)
	return t
}

func ConfirmRedownload(filename string) bool {
	if !isatty.IsTerminal(os.Stdout.Fd()) {
		return true
	}
	labelStyle := lipgloss.NewStyle().Foreground(colorAmber).Bold(true)
	fileStyle := lipgloss.NewStyle().Foreground(colorPhosphor).Bold(true)

	fmt.Println()
	fmt.Println(labelStyle.Render("  ⚠  file already on disk") + " " +
		styleSep.Render("│") + " " + fileStyle.Render(filename))
	fmt.Println()

	var proceed bool
	f := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Overwrite and re-download?").
				Description("The existing file will be replaced.").
				Value(&proceed).
				Affirmative("overwrite").
				Negative("keep existing"),
		),
	).WithTheme(carrierTheme())
	_ = f.Run()
	return proceed
}

const helpMarkdown = "" +
	"# hget\n" +
	"\n" +
	"_carrier signal · multi-stream telemetry · resumable_\n" +
	"\n" +
	"## Usage\n" +
	"\n" +
	"```\n" +
	"hget [options] <url>\n" +
	"hget [options] --resume=<task-name>\n" +
	"hget --file=<urls-file> [options]\n" +
	"```\n" +
	"\n" +
	"## Options\n" +
	"\n" +
	"| Flag                | Description                                            | Default     |\n" +
	"| ------------------- | ------------------------------------------------------ | ----------- |\n" +
	"| `-n <int>`          | number of parallel connections                         | _# of CPUs_ |\n" +
	"| `--skip-tls`        | skip TLS certificate verification                      | `false`     |\n" +
	"| `--proxy <addr>`    | proxy (`socks5: host:port` or `http://host:port`)      |             |\n" +
	"| `--file <path>`     | path to a file containing one URL per line             |             |\n" +
	"| `--rate <limit>`    | bandwidth cap per download (e.g. `10kB`, `5MiB`)       |             |\n" +
	"| `--resume <task>`   | resume a stopped download by task name or URL          |             |\n" +
	"| `--probe <url>`     | probe URL for range support & content-length only      |             |\n" +
	"| `--timeout <dur>`   | timeout waiting for response headers (e.g. `30s`)      | `15s`       |\n" +
	"| `--verify`          | download & GPG-verify the `.sig` signature file        | `false`     |\n" +
	"| `--extractor <m>`   | extractor mode: `auto` / `yt-dlp` / `none`             | `auto`      |\n" +
	"| `--cookies <path>`  | cookies.txt for the extractor (yt-dlp `--cookies`)     |             |\n" +
	"| `--cookies-from-browser <s>` | extract cookies from browser (e.g. `firefox`, `chrome:Default`) | |\n" +
	"| `--quality <preset>` | extractor quality preset: `360p` / `480p` / `720p` / `1080p` / `1440p` / `4K` / `8K` / `best` / `audio` | `720p` |\n" +
	"| `--container <ext>` | extractor output container: `mp4` / `mkv` / `webm` | `mp4` |\n" +
	"| `--audio-lang <code>` | preferred audio language (forwarded as yt-dlp `-S lang:<code>`); empty disables the bias | `en` |\n" +
	"| `--pick-format`     | open the VCR rocker UI to choose resolution / audio / container by hand | `false` |\n" +
	"\n" +
	"## Extractor mode (yt-dlp pipeline)\n" +
	"\n" +
	"When the URL points at a media host (YouTube, Vimeo, Twitch, …) hget hands\n" +
	"off to `yt-dlp` and renders a retro VCR panel instead of the data-link.\n" +
	"By default the deck goes straight from probing to recording at the\n" +
	"`--quality` preset (720p mp4) with English audio — no rocker UI, no\n" +
	"jumpscreens.  yt-dlp gets `-S lang:en` so YouTube's auto-translated\n" +
	"audio tracks don't win over the original-language stream.\n" +
	"\n" +
	"Pass `--pick-format` to opt into the rocker UI: after probing, the\n" +
	"deck enters **browsing mode** with three rockers (video / audio /\n" +
	"container) on the same chassis.  Press REC (`enter` / `r`) to engage.\n" +
	"In batch mode the first tape's pick is locked in for the whole queue\n" +
	"and applied adaptively to videos that don't share the same format IDs.\n" +
	"\n" +
	"### Browsing-mode keys (with `--pick-format`)\n" +
	"\n" +
	"| Key                  | Rocker                                          |\n" +
	"| -------------------- | ----------------------------------------------- |\n" +
	"| `↑` / `k`            | tape (video) — cycle to higher quality          |\n" +
	"| `↓` / `j`            | tape (video) — cycle to lower quality           |\n" +
	"| `←` / `h`            | audio track — previous                          |\n" +
	"| `→` / `l`            | audio track — next                              |\n" +
	"| `tab` / `f`          | container — `mp4` / `mkv` / `webm`              |\n" +
	"| `shift+tab`          | container — reverse                             |\n" +
	"| `enter` / `r`        | **REC** — commit selection and start download   |\n" +
	"| `q` / `ctrl+c`       | abort and exit                                  |\n" +
	"\n" +
	"Progressive streams (Twitter, etc.) collapse the audio rocker into\n" +
	"`(included)`. Live streams skip the selector entirely and engage\n" +
	"yt-dlp's `best` format on sight.\n" +
	"\n" +
	"### Batch mode (a file of YouTube URLs)\n" +
	"\n" +
	"`--file urls.txt` is **all-or-nothing**: if any URL in the file looks\n" +
	"extractable (or `--extractor=yt-dlp` is forced), the whole list is\n" +
	"routed through yt-dlp.  A horizontal **cassette shelf** appears above\n" +
	"the VCR — one VHS tape per URL, with the active tape lifted out of its\n" +
	"slot.  Plain HTTP URLs go to yt-dlp's generic extractor.\n" +
	"\n" +
	"Every tape uses the same `--quality` preset by default.  Probes run\n" +
	"in parallel so spine labels — title, channel chip, runtime,\n" +
	"resolution — resolve ahead of the deck reaching their position.  The\n" +
	"shelf scales down (or up) automatically based on terminal width: full\n" +
	"detail on wide terminals, compact tape spines on narrow ones.\n" +
	"\n" +
	"Failed tapes get a magenta `✗` sticker and the queue continues to the\n" +
	"next URL — failures never abort the batch.\n" +
	"\n" +
	"## Examples\n" +
	"\n" +
	"```bash\n" +
	"# basic download\n" +
	"hget https://example.com/file.iso\n" +
	"\n" +
	"# 8 connections, 5 MiB/s cap\n" +
	"hget -n 8 --rate 5MiB https://example.com/large.tar.gz\n" +
	"\n" +
	"# resume an interrupted download\n" +
	"hget --resume https://example.com/file.iso\n" +
	"\n" +
	"# batch download from a file\n" +
	"hget --file urls.txt\n" +
	"\n" +
	"# probe server without downloading\n" +
	"hget --probe https://example.com/file.iso\n" +
	"\n" +
	"# download & verify GPG signature\n" +
	"hget --verify https://example.com/file.iso\n" +
	"\n" +
	"# YouTube / Vimeo / Twitch via yt-dlp (auto-detected) — VCR + Mixer TUI\n" +
	"hget https://www.youtube.com/watch?v=dQw4w9WgXcQ\n" +
	"hget --extractor yt-dlp https://example.com/some-stream\n" +
	"\n" +
	"# YouTube with a cookies.txt file (bypasses the bot challenge)\n" +
	"hget --cookies ~/cookies.txt https://www.youtube.com/watch?v=dQw4w9WgXcQ\n" +
	"\n" +
	"# YouTube using your live browser cookies (no export needed)\n" +
	"hget --cookies-from-browser firefox https://www.youtube.com/watch?v=dQw4w9WgXcQ\n" +
	"\n" +
	"# batch of YouTube URLs — VCR + cassette-shelf TUI, 720p mp4 default\n" +
	"hget --file videos.txt\n" +
	"\n" +
	"# YouTube at 1080p mkv, keep English audio (skips auto-translations)\n" +
	"hget --quality 1080p --container mkv https://www.youtube.com/watch?v=dQw4w9WgXcQ\n" +
	"\n" +
	"# audio-only — pulls best audio track in original language\n" +
	"hget --quality audio https://www.youtube.com/watch?v=dQw4w9WgXcQ\n" +
	"\n" +
	"# manually pick resolution/audio/container via the VCR rockers\n" +
	"hget --pick-format https://www.youtube.com/watch?v=dQw4w9WgXcQ\n" +
	"```\n"

func PrintHelp() {
	// ── Banner (unchanged ANSI wordmark) ─────────────────────────────────
	fmt.Fprintln(Stdout, styleBanner.Render(banner))
	fmt.Fprintln(Stdout, lipgloss.NewStyle().Foreground(colorSteel).Render(bannerStrap))
	fmt.Fprintln(Stdout, lipgloss.NewStyle().Foreground(colorPhosphor).Render(strings.Repeat("═", 68)))
	fmt.Fprintln(Stdout)

	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(72),
	)
	if err != nil {
		// Fallback: print raw markdown when glamour can't init (no TTY,
		// missing chroma styles, etc.) — still readable.
		fmt.Fprint(Stdout, helpMarkdown)
		return
	}
	out, err := r.Render(helpMarkdown)
	if err != nil {
		fmt.Fprint(Stdout, helpMarkdown)
		return
	}
	fmt.Fprint(Stdout, out)
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
