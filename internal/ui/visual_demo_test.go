package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestMainframeSnapshot(t *testing.T) {
	if testing.Short() {
		t.Skip("visual demo")
	}
	states := []struct {
		name string
		s    mainframeState
	}{
		{"IDLE", mfIdle},
		{"HANDSHAKING", mfHandshaking},
		{"TRANSFERRING", mfTransferring},
		{"COMPLETE", mfComplete},
		{"ALARM", mfAlarm},
	}
	for _, st := range states {
		mf := newMainframe()
		mf.SetState(st.s)
		// Settle a few frames so LEDs have density.
		for i := 0; i < 12; i++ {
			mf.Tick()
		}
		fmt.Printf("\n─── %s ───\n", st.name)
		fmt.Println(mf.View())
	}
}

func TestMainframeWidthInvariant(t *testing.T) {
	mf := newMainframe()
	mf.SetState(mfTransferring)
	for i := 0; i < 8; i++ {
		mf.Tick()
	}
	out := mf.View()
	for i, line := range strings.Split(out, "\n") {
		if w := visibleWidth(line); w != mainframeWidth {
			t.Errorf("line %d: width = %d, want %d\n  line: %q", i, w, mainframeWidth, line)
		}
	}
}

// TestTapeBannerSnapshot prints each tape state — banner mode.
func TestTapeBannerSnapshot(t *testing.T) {
	if testing.Short() {
		t.Skip("visual demo")
	}
	cases := []struct {
		name     string
		s        tapeState
		progress float64
		speed    float64
	}{
		{"IDLE", tapeIdle, 0, 0},
		{"MOUNTING", tapeMounting, 0, 0},
		{"TRANSFERRING 38%", tapeTransferring, 0.38, 1_200_000},
		{"TRANSFERRING 91%", tapeTransferring, 0.91, 4_500_000},
		{"COMPLETE", tapeComplete, 1.0, 0},
		{"DISCONNECTED", tapeDisconnected, 0.45, 0},
	}
	for _, c := range cases {
		tp := newTape("TAPE-01")
		tp.SetState(c.s)
		tp.Update(c.progress, c.speed, 5_000_000)
		for i := 0; i < 8; i++ {
			tp.Tick()
		}
		fmt.Printf("\n─── %s ───\n", c.name)
		fmt.Println(tp.ViewBanner())
	}
}

// TestTapeMiniSnapshot prints SAN-mode tapes side-by-side.
func TestTapeMiniSnapshot(t *testing.T) {
	if testing.Short() {
		t.Skip("visual demo")
	}
	tapes := []tape{
		newTape("TAPE-01"),
		newTape("TAPE-02"),
		newTape("TAPE-03"),
		newTape("TAPE-04"),
		newTape("TAPE-05"),
	}
	tapes[0].SetState(tapeComplete)
	tapes[0].Update(1.0, 0, 0)
	tapes[1].SetState(tapeComplete)
	tapes[1].Update(1.0, 0, 0)
	tapes[2].SetState(tapeTransferring)
	tapes[2].Update(0.47, 1_500_000, 5_000_000)
	tapes[3].SetState(tapeIdle)
	tapes[4].SetState(tapeIdle)
	for i := range tapes {
		for j := 0; j < 6; j++ {
			tapes[i].Tick()
		}
	}
	views := make([]string, len(tapes))
	for i := range tapes {
		views[i] = tapes[i].ViewMini()
	}
	// horizontal join — emulate side-by-side rendering
	rows := make([][]string, len(tapes))
	for i, v := range views {
		rows[i] = strings.Split(v, "\n")
	}
	fmt.Println("\n─── SAN ROW (mini tapes) ───")
	for line := 0; line < len(rows[0]); line++ {
		var b strings.Builder
		for i := range rows {
			b.WriteString(rows[i][line])
			b.WriteString("  ")
		}
		fmt.Println(b.String())
	}
}

// TestTapeBannerWidthInvariant ensures every banner row equals tapeBannerWidth.
func TestTapeBannerWidthInvariant(t *testing.T) {
	tp := newTape("TAPE-01")
	tp.SetState(tapeTransferring)
	tp.Update(0.42, 2_000_000, 5_000_000)
	for i := 0; i < 6; i++ {
		tp.Tick()
	}
	for i, line := range strings.Split(tp.ViewBanner(), "\n") {
		if w := visibleWidth(line); w != tapeBannerWidth {
			t.Errorf("banner row %d: width=%d want=%d\n  line=%q", i, w, tapeBannerWidth, line)
		}
	}
}

// TestTapeMiniWidthInvariant ensures every mini row equals tapeMiniWidth.
func TestTapeMiniWidthInvariant(t *testing.T) {
	tp := newTape("TAPE-XX")
	tp.SetState(tapeTransferring)
	tp.Update(0.42, 1_000_000, 3_000_000)
	for i := 0; i < 6; i++ {
		tp.Tick()
	}
	for i, line := range strings.Split(tp.ViewMini(), "\n") {
		if w := visibleWidth(line); w != tapeMiniWidth {
			t.Errorf("mini row %d: width=%d want=%d\n  line=%q", i, w, tapeMiniWidth, line)
		}
	}
}

// TestModemHandshakeFullSnapshot renders each phase of the new modem scene.
func TestModemHandshakeFullSnapshot(t *testing.T) {
	if testing.Short() {
		t.Skip("visual demo")
	}
	m := newModemHandshake()
	m.SetWidth(96)
	url := "https://example.com/very/long/path/to/a/large-archive.tar.gz"
	phases := []struct {
		name   string
		frames int
	}{
		{"DIALING", 14},
		{"CARRIER DETECT", 15},
		{"HANDSHAKE", 15},
		{"LINK ESTABLISHED", 6},
	}
	for _, p := range phases {
		for i := 0; i < p.frames; i++ {
			m.Tick()
		}
		fmt.Printf("\n─── %s ───\n", p.name)
		fmt.Println(m.View(url))
	}
}

// TestSANSnapshot renders the storage-array cabinet at full size.
func TestSANSnapshot(t *testing.T) {
	if testing.Short() {
		t.Skip("visual demo")
	}
	items := []sanItem{
		{Label: "ubuntu-22.04.iso", Status: sanDone},
		{Label: "Prisoner.S01E01.mkv", Status: sanDone},
		{Label: "Prisoner.S01E02.mkv", Status: sanActive},
		{Label: "T-04", Status: sanQueued},
		{Label: "T-05", Status: sanQueued},
		{Label: "T-06", Status: sanQueued},
	}
	s := newSan(items)
	s.SetWidth(110)
	s.SetActive(2)
	s.Update(0.47, 1_500_000, 5_000_000, mfTransferring, cableActive)
	for i := 0; i < 8; i++ {
		s.Tick(0.4)
	}
	fmt.Println("\n─── STORAGE ARRAY CABINET ───")
	fmt.Println(s.View())
}

// TestSANCompactSnapshot renders the SAN in compact (no-chassis) mode.
func TestSANCompactSnapshot(t *testing.T) {
	if testing.Short() {
		t.Skip("visual demo")
	}
	items := []sanItem{
		{Label: "ubuntu-22.04.iso", Status: sanDone},
		{Label: "Prisoner.S01E02.mkv", Status: sanActive},
		{Label: "T-03", Status: sanQueued},
	}
	s := newSan(items)
	s.SetWidth(110)
	s.SetCompact(true)
	s.SetActive(1)
	s.Update(0.47, 1_500_000, 5_000_000, mfTransferring, cableActive)
	for i := 0; i < 8; i++ {
		s.Tick(0.4)
	}
	fmt.Println("\n─── STORAGE ARRAY (compact, no chassis) ───")
	fmt.Println(s.View())
}

// TestIntegratedDownloadView renders the composed mainframe + cable + tape
// banner + datalink panel as a single tuiModel.View() snapshot, so the full
// stack can be eyeballed at once.
func TestIntegratedDownloadView(t *testing.T) {
	if testing.Short() {
		t.Skip("visual demo")
	}
	m := NewTUIModel(4, false, 0, 0, nil, nil)
	m.width = 110
	m.height = 60 // generous so tier=full
	m.url = "https://example.com/very/long/path/to/large-archive.tar.gz"
	m.fileName = "large-archive.tar.gz"
	m.size = 100_000_000
	m.totalDown = 47_000_000
	m.overallPct = 0.47
	m.peakSpeed = 5_900_000
	m.startTime = m.startTime.Add(0)
	m.started = true
	m.parts = []partModel{
		{total: 25_000_000, downloaded: 12_500_000, smoothPct: 0.50, speed: 1_500_000},
		{total: 25_000_000, downloaded: 11_750_000, smoothPct: 0.47, speed: 1_400_000},
		{total: 25_000_000, downloaded: 11_500_000, smoothPct: 0.46, speed: 1_300_000},
		{total: 25_000_000, downloaded: 11_250_000, smoothPct: 0.45, speed: 1_400_000},
	}
	// Settle a few frames so animations have density.
	for i := 0; i < 12; i++ {
		mod, _ := m.Update(tickMsg(time.Now()))
		m = mod.(tuiModel)
	}
	fmt.Println("\n─── INTEGRATED DOWNLOAD VIEW (single, w=110) ───")
	fmt.Println(m.View())
}

// TestIntegratedBatchView renders the SAN view above the data-link panel.
func TestIntegratedBatchView(t *testing.T) {
	if testing.Short() {
		t.Skip("visual demo")
	}
	m := NewTUIModel(4, false, 3, 6, func() {}, nil)
	m.width = 110
	m.height = 60
	m.url = "https://example.com/file3.iso"
	m.fileName = "file3.iso"
	m.size = 100_000_000
	m.totalDown = 23_000_000
	m.overallPct = 0.23
	m.peakSpeed = 5_900_000
	m.started = true
	m.parts = []partModel{
		{total: 25_000_000, downloaded: 6_000_000, smoothPct: 0.24, speed: 800_000},
		{total: 25_000_000, downloaded: 5_500_000, smoothPct: 0.22, speed: 700_000},
		{total: 25_000_000, downloaded: 6_000_000, smoothPct: 0.24, speed: 850_000},
		{total: 25_000_000, downloaded: 5_500_000, smoothPct: 0.22, speed: 700_000},
	}
	for i := 0; i < 12; i++ {
		mod, _ := m.Update(tickMsg(time.Now()))
		m = mod.(tuiModel)
	}
	fmt.Println("\n─── INTEGRATED BATCH/SAN VIEW (item 3 of 6, w=110) ───")
	fmt.Println(m.View())
}

// TestIntegratedSkippingView shows the disconnected state across all scene
// elements (mainframe alarm, cable broken, tape disconnected).
func TestIntegratedSkippingView(t *testing.T) {
	if testing.Short() {
		t.Skip("visual demo")
	}
	m := NewTUIModel(4, false, 0, 0, nil, nil)
	m.width = 110
	m.height = 60
	m.url = "https://example.com/file.iso"
	m.fileName = "file.iso"
	m.size = 100_000_000
	m.totalDown = 30_000_000
	m.overallPct = 0.30
	m.peakSpeed = 5_900_000
	m.started = true
	m.skipping = true
	m.parts = []partModel{
		{total: 25_000_000, downloaded: 8_000_000, smoothPct: 0.32, speed: 0},
		{total: 25_000_000, downloaded: 7_500_000, smoothPct: 0.30, speed: 0},
		{total: 25_000_000, downloaded: 7_500_000, smoothPct: 0.30, speed: 0},
		{total: 25_000_000, downloaded: 7_000_000, smoothPct: 0.28, speed: 0},
	}
	for i := 0; i < 16; i++ {
		mod, _ := m.Update(tickMsg(time.Now()))
		m = mod.(tuiModel)
	}
	fmt.Println("\n─── INTEGRATED SKIPPING VIEW (link severed) ───")
	fmt.Println(m.View())
}

// TestLayoutTiers verifies that the layout tier picker chooses the right
// scene composition at canonical terminal sizes.
func TestLayoutTiers(t *testing.T) {
	cases := []struct {
		name     string
		w, h     int
		wantTier layoutTier
	}{
		{"huge", 200, 60, tierFull},
		{"normal-tall", 110, 52, tierFull},
		{"wide-medium", 160, 40, tierSideBySide},
		{"normal-medium", 110, 36, tierTapeOnly},
		{"narrow-tape", 90, 32, tierTapeOnly},
		{"too-short", 110, 22, tierMinimal},
		{"too-narrow", 70, 50, tierMinimal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := NewTUIModel(4, false, 0, 0, nil, nil)
			m.width = c.w
			m.height = c.h
			m.parts = make([]partModel, 4)
			got := m.computeTier()
			if got != c.wantTier {
				t.Errorf("computeTier(w=%d,h=%d) = %d, want %d", c.w, c.h, got, c.wantTier)
			}
		})
	}
}

// TestSceneSideBySide renders the wide+short scene and confirms total
// rendered height is bounded.
func TestSceneSideBySide(t *testing.T) {
	if testing.Short() {
		t.Skip("visual demo")
	}
	m := NewTUIModel(4, false, 0, 0, nil, nil)
	m.width = 160
	m.height = 42
	m.url = "https://example.com/file.iso"
	m.fileName = "file.iso"
	m.size = 100_000_000
	m.totalDown = 47_000_000
	m.overallPct = 0.47
	m.peakSpeed = 5_900_000
	m.started = true
	m.parts = []partModel{
		{total: 25_000_000, downloaded: 12_500_000, smoothPct: 0.50, speed: 1_500_000},
		{total: 25_000_000, downloaded: 11_750_000, smoothPct: 0.47, speed: 1_400_000},
		{total: 25_000_000, downloaded: 11_500_000, smoothPct: 0.46, speed: 1_300_000},
		{total: 25_000_000, downloaded: 11_250_000, smoothPct: 0.45, speed: 1_400_000},
	}
	for i := 0; i < 12; i++ {
		mod, _ := m.Update(tickMsg(time.Now()))
		m = mod.(tuiModel)
	}
	out := m.View()
	rows := strings.Count(out, "\n")
	fmt.Printf("\n─── SIDE-BY-SIDE (w=%d, h=%d, rows=%d) ───\n", m.width, m.height, rows)
	fmt.Println(out)
	if rows > m.height {
		t.Errorf("rendered %d rows; terminal height is %d", rows, m.height)
	}
}

// TestSceneTapeOnly renders the tape-only tier and verifies it fits.
func TestSceneTapeOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("visual demo")
	}
	m := NewTUIModel(4, false, 0, 0, nil, nil)
	m.width = 110
	m.height = 30
	m.url = "https://example.com/file.iso"
	m.fileName = "file.iso"
	m.size = 100_000_000
	m.totalDown = 47_000_000
	m.overallPct = 0.47
	m.peakSpeed = 5_900_000
	m.started = true
	m.parts = []partModel{
		{total: 25_000_000, downloaded: 12_500_000, smoothPct: 0.50, speed: 1_500_000},
		{total: 25_000_000, downloaded: 11_750_000, smoothPct: 0.47, speed: 1_400_000},
		{total: 25_000_000, downloaded: 11_500_000, smoothPct: 0.46, speed: 1_300_000},
		{total: 25_000_000, downloaded: 11_250_000, smoothPct: 0.45, speed: 1_400_000},
	}
	for i := 0; i < 12; i++ {
		mod, _ := m.Update(tickMsg(time.Now()))
		m = mod.(tuiModel)
	}
	out := m.View()
	rows := strings.Count(out, "\n")
	fmt.Printf("\n─── TAPE-ONLY (w=110, h=30, rows=%d) ───\n", rows)
	fmt.Println(out)
	if rows > m.height {
		t.Errorf("rendered %d rows; terminal height is %d", rows, m.height)
	}
}

func TestSkipScreen(t *testing.T) {
	if testing.Short() {
		t.Skip("visual demo")
	}
	m := NewTUIModel(4, false, 2, 4, func() {}, nil)
	m.width = 110
	m.height = 50
	m.url = "https://example.com/Seeking.Persephone.S01E04.mkv"
	m.fileName = "Seeking.Persephone.S01E04.mkv"
	m.size = 526_300_000
	m.totalDown = 12_000_000
	m.peakSpeed = 5_900_000
	m.started = true
	m.skipping = true
	m.skipped = true
	m.parts = []partModel{
		{total: 130_000_000, downloaded: 3_000_000, smoothPct: 0.02, speed: 0},
		{total: 130_000_000, downloaded: 3_000_000, smoothPct: 0.02, speed: 0},
		{total: 130_000_000, downloaded: 3_000_000, smoothPct: 0.02, speed: 0},
		{total: 130_000_000, downloaded: 3_000_000, smoothPct: 0.02, speed: 0},
	}
	for i := 0; i < 16; i++ {
		mod, _ := m.Update(tickMsg(time.Now()))
		m = mod.(tuiModel)
	}
	out := m.View()
	rows := strings.Count(out, "\n")
	fmt.Printf("\n─── SKIP SCREEN (w=%d, h=%d, rows=%d) ───\n", m.width, m.height, rows)
	fmt.Println(out)

	if strings.Contains(out, "LINK FAILED") {
		t.Error("skip screen should not render 'LINK FAILED'")
	}
	if !strings.Contains(out, "TRANSFER SKIPPED") {
		t.Error("skip screen should render 'TRANSFER SKIPPED' caption")
	}
	if rows > m.height+2 {
		t.Errorf("rendered %d rows; terminal height is %d", rows, m.height)
	}
}

// TestSkipDetectionViaError verifies that a DownloadErrorMsg carrying a
// skip error string is routed to the skip screen, not the failure screen.
func TestSkipDetectionViaError(t *testing.T) {
	m := NewTUIModel(4, false, 1, 1, nil, nil)
	m.width = 110
	m.height = 50
	m.skipping = true // simulates 's' having been pressed
	skipErr := errors.New("skip current item")
	mod, _ := m.Update(DownloadErrorMsg{Err: skipErr})
	m = mod.(tuiModel)
	if m.hasError {
		t.Error("skip should not set hasError")
	}
	if !m.skipped {
		t.Error("skip should set m.skipped")
	}
}

func TestProgressUnknownSize(t *testing.T) {
	m := NewTUIModel(4, false, 0, 0, nil, nil)
	m.width = 110
	m.height = 60
	m.size = 0 // server returned no Content-Length
	m.totalDown = 0
	m.started = true
	m.parts = []partModel{
		{total: 25_000_000, downloaded: 12_500_000, speed: 1_500_000},
		{total: 25_000_000, downloaded: 11_750_000, speed: 1_400_000},
		{total: 25_000_000, downloaded: 11_500_000, speed: 1_300_000},
		{total: 25_000_000, downloaded: 11_250_000, speed: 1_400_000},
	}
	// Drive a few ticks so the spring settles.
	for i := 0; i < 80; i++ {
		mod, _ := m.Update(tickMsg(time.Now()))
		m = mod.(tuiModel)
	}
	if m.overallPct < 0.30 {
		t.Errorf("overallPct = %.2f, want > 0.30 (sum of per-part totals "+
			"should drive the bar even when m.size == 0)", m.overallPct)
	}
}

func TestSANDynamicSizing(t *testing.T) {
	if testing.Short() {
		t.Skip("visual demo")
	}
	cases := []struct {
		name string
		w, h int
		// Expected layout characteristics:
		wantChassis bool // is the cabinet frame drawn?
	}{
		{"big-12-items", 110, 60, true},
		{"medium-12-items", 110, 36, false},     // compact only
		{"short-12-items", 110, 30, false},      // compact only
		{"very-short-12-items", 110, 26, false}, // compact only
		{"narrow-12-items", 80, 50, true},       // cabinet fits
		{"too-tiny", 110, 24, false},            // datalink only, no SAN
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := NewTUIModel(4, false, 5, 12, func() {}, nil)
			m.width = c.w
			m.height = c.h
			m.url = "https://example.com/file5.iso"
			m.fileName = "Prisoner.S01E05.mkv"
			m.size = 100_000_000
			m.totalDown = 47_000_000
			m.peakSpeed = 5_900_000
			m.started = true
			m.parts = []partModel{
				{total: 25_000_000, downloaded: 12_500_000, smoothPct: 0.50, speed: 1_500_000},
				{total: 25_000_000, downloaded: 11_750_000, smoothPct: 0.47, speed: 1_400_000},
				{total: 25_000_000, downloaded: 11_500_000, smoothPct: 0.46, speed: 1_300_000},
				{total: 25_000_000, downloaded: 11_250_000, smoothPct: 0.45, speed: 1_400_000},
			}
			for i := 0; i < 12; i++ {
				mod, _ := m.Update(tickMsg(time.Now()))
				m = mod.(tuiModel)
			}
			out := m.View()
			rows := strings.Count(out, "\n")
			fmt.Printf("\n─── %s (w=%d, h=%d, rows=%d) ───\n", c.name, c.w, c.h, rows)
			fmt.Println(out)
			if rows > c.h {
				t.Errorf("rendered %d rows; terminal height is %d", rows, c.h)
			}
			hasChassis := strings.Contains(out, "STORAGE ARRAY")
			if hasChassis != c.wantChassis {
				t.Errorf("hasChassis=%v want=%v", hasChassis, c.wantChassis)
			}
		})
	}
}

// TestSceneSANCompact verifies the SAN drops the mainframe block when
// terminal height forces tapeOnly tier in batch mode.
func TestSceneSANCompact(t *testing.T) {
	if testing.Short() {
		t.Skip("visual demo")
	}
	m := NewTUIModel(4, false, 3, 6, func() {}, nil)
	m.width = 110
	m.height = 34 // tierTapeOnly with batch banner accounted for
	m.url = "https://example.com/file3.iso"
	m.fileName = "file3.iso"
	m.size = 100_000_000
	m.totalDown = 23_000_000
	m.overallPct = 0.23
	m.peakSpeed = 5_900_000
	m.started = true
	m.parts = []partModel{
		{total: 25_000_000, downloaded: 6_000_000, smoothPct: 0.24, speed: 800_000},
		{total: 25_000_000, downloaded: 5_500_000, smoothPct: 0.22, speed: 700_000},
		{total: 25_000_000, downloaded: 6_000_000, smoothPct: 0.24, speed: 850_000},
		{total: 25_000_000, downloaded: 5_500_000, smoothPct: 0.22, speed: 700_000},
	}
	for i := 0; i < 12; i++ {
		mod, _ := m.Update(tickMsg(time.Now()))
		m = mod.(tuiModel)
	}
	out := m.View()
	rows := strings.Count(out, "\n")
	fmt.Printf("\n─── BATCH/SAN COMPACT (w=%d, h=%d, rows=%d) ───\n", m.width, m.height, rows)
	fmt.Println(out)
	if rows > m.height {
		t.Errorf("rendered %d rows; terminal height is %d", rows, m.height)
	}
	if strings.Contains(out, "MASTER CONSOLE") {
		t.Errorf("expected mainframe to be omitted in compact tier; got it in output")
	}
}

// TestSceneMinimal renders the minimal tier (data-link only).
func TestSceneMinimal(t *testing.T) {
	if testing.Short() {
		t.Skip("visual demo")
	}
	m := NewTUIModel(4, false, 0, 0, nil, nil)
	m.width = 110
	m.height = 22
	m.url = "https://example.com/file.iso"
	m.fileName = "file.iso"
	m.size = 100_000_000
	m.totalDown = 47_000_000
	m.overallPct = 0.47
	m.peakSpeed = 5_900_000
	m.started = true
	m.parts = []partModel{
		{total: 25_000_000, downloaded: 12_500_000, smoothPct: 0.50, speed: 1_500_000},
		{total: 25_000_000, downloaded: 11_750_000, smoothPct: 0.47, speed: 1_400_000},
		{total: 25_000_000, downloaded: 11_500_000, smoothPct: 0.46, speed: 1_300_000},
		{total: 25_000_000, downloaded: 11_250_000, smoothPct: 0.45, speed: 1_400_000},
	}
	for i := 0; i < 12; i++ {
		mod, _ := m.Update(tickMsg(time.Now()))
		m = mod.(tuiModel)
	}
	out := m.View()
	rows := strings.Count(out, "\n")
	fmt.Printf("\n─── MINIMAL (w=110, h=22, rows=%d) ───\n", rows)
	fmt.Println(out)
	if rows > m.height {
		t.Errorf("rendered %d rows; terminal height is %d", rows, m.height)
	}
}

// visibleWidth strips ANSI escape sequences and returns the display width
// using lipgloss's width-aware accounting.
func visibleWidth(s string) int {
	// Strip ANSI CSI sequences (\x1b[...m).
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j < len(s) {
				j++ // consume final byte
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	// Count runes; all our glyphs are width-1.
	return len([]rune(b.String()))
}
