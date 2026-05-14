package ui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/harmonica"
	"github.com/charmbracelet/lipgloss"
)

const vaultWidth = 70

// vaultMode is the verify panel's operational state.
type vaultMode int

const (
	vaultScanning vaultMode = iota
	vaultVerified
	vaultBreached
)

type VerifyDetails struct {
	SignedBy    string // user id, e.g. "Linus Torvalds <torvalds@kernel.org>"
	KeyID       string // long key id, e.g. "ABCDEF1234567890"
	Fingerprint string // 40-hex fingerprint with spaces every 4 chars
	SignedAt    string // signature timestamp string
	Verdict     string // short verdict label rendered in the verdict row
	Raw         string // raw gpg output, kept for the "details" pane
}

type verifyAnimation struct {
	frame    int
	mode     vaultMode
	details  VerifyDetails
	scanBar  progress.Model
	scanPct  float64
	scanVel  float64
	scanSpr  harmonica.Spring
	scanTgt  float64
	keyFrame int
	width    int
}

func newVerifyAnimation() verifyAnimation {
	bar := progress.New(
		progress.WithGradient("#1E7A99", "#73E0FF"),
		progress.WithoutPercentage(),
		progress.WithWidth(vaultWidth-26),
	)
	return verifyAnimation{
		mode:    vaultScanning,
		scanBar: bar,
		scanSpr: harmonica.NewSpring(harmonica.FPS(60), 7.0, 0.85),
		scanTgt: 0.78, // scan never quite saturates while we're still waiting
		width:   vaultWidth,
	}
}

// SetVerified marks the panel as verified and stores parsed details.
func (v *verifyAnimation) SetVerified(d VerifyDetails) {
	v.mode = vaultVerified
	v.details = d
	v.scanTgt = 1.0
}

// SetBreached marks the panel as failed and stores parsed details.
func (v *verifyAnimation) SetBreached(d VerifyDetails) {
	v.mode = vaultBreached
	v.details = d
	v.scanTgt = 1.0
}

// Mode returns the current operational mode.
func (v verifyAnimation) Mode() vaultMode { return v.mode }

// Frame returns the current animation frame counter.
func (v verifyAnimation) Frame() int { return v.frame }

func (v *verifyAnimation) Tick() {
	v.frame++
	v.keyFrame = (v.frame / 3) % 4
	v.scanPct, v.scanVel = v.scanSpr.Update(v.scanPct, v.scanVel, v.scanTgt)
	if v.scanPct < 0 {
		v.scanPct = 0
	}
	if v.scanPct > 1 {
		v.scanPct = 1
	}
}

// View — renders the entire vault panel.
func (v verifyAnimation) View() string {
	chrome := fgStyle(colorPhosphor)
	frame := fgStyle(colorSlate)
	steel := fgStyle(colorSteel)
	frost := fgBoldStyle(colorFrost)
	brand := fgBoldStyle(colorAmber)

	// Per-mode accent colours.
	var accent lipgloss.Color
	var statusStr string
	var statusCol lipgloss.Color
	var verdict string
	var verdictCol lipgloss.Color
	switch v.mode {
	case vaultScanning:
		accent, statusStr, statusCol = colorAmber, "ANALYZING", colorAmber
		verdict, verdictCol = "scanning…", colorAmber
	case vaultVerified:
		accent, statusStr, statusCol = colorMint, "SIGNATURE VALID", colorMint
		verdict, verdictCol = "◆ VALID", colorMint
		if v.details.Verdict != "" {
			verdict = "◆ " + v.details.Verdict
		}
	case vaultBreached:
		accent, statusStr, statusCol = colorMagenta, "INTEGRITY FAULT", colorMagenta
		verdict, verdictCol = "◈ INVALID", colorMagenta
		if v.details.Verdict != "" {
			verdict = "◈ " + v.details.Verdict
		}
	}

	inner := vaultWidth - 2
	pad := func(s string, w int) string {
		gap := w - lipgloss.Width(s)
		if gap < 0 {
			gap = 0
		}
		return s + strings.Repeat(" ", gap)
	}
	centre := func(s string, w int) string {
		gap := w - lipgloss.Width(s)
		if gap < 0 {
			gap = 0
		}
		l := gap / 2
		r := gap - l
		return strings.Repeat(" ", l) + s + strings.Repeat(" ", r)
	}

	var b strings.Builder

	// ── Row 0: top frame. ───────────────────────────────────────────────
	b.WriteString(chrome.Render("╔") +
		chrome.Render(strings.Repeat("═", inner)) +
		chrome.Render("╗") + "\n")

	// ── Row 1: brand plate. ─────────────────────────────────────────────
	plate := brand.Render("▓▓ HGET·VAULT/CRYPTO ▓▓") +
		steel.Render("  GPG·VERIFIER  ") +
		frost.Render("S/N 2048")
	b.WriteString(chrome.Render("║") + centre(plate, inner) + chrome.Render("║") + "\n")

	// ── Row 2: divider. ─────────────────────────────────────────────────
	b.WriteString(chrome.Render("╠") +
		frame.Render(strings.Repeat("═", inner)) +
		chrome.Render("╣") + "\n")

	// ── Row 3: status caption with mode-driven LEDs. ────────────────────
	pwrOn := true
	keyOn := v.mode != vaultBreached
	scanOn := v.mode == vaultScanning && (v.frame/4)%2 == 0
	alarmOn := v.mode == vaultBreached && (v.frame/6)%2 == 0
	if v.mode == vaultVerified {
		// steady "key" + "scan" green
		scanOn = true
	}
	chip := func(name string, on bool, col lipgloss.Color) string {
		c := colorSlate
		if on {
			c = col
		}
		return fgBoldStyle(c).Render("◉") + " " + steel.Render(name)
	}
	chips := strings.Join([]string{
		chip("POWER", pwrOn, colorMint),
		chip("KEY", keyOn, colorPhosphor),
		chip("SCAN", scanOn, colorAmber),
		chip("ALARM", alarmOn, colorMagenta),
	}, "  ")
	b.WriteString(chrome.Render("║ ") + pad("[ "+steel.Render("SIGNATURE ANALYZER")+" ]  "+chips, inner-2) + chrome.Render(" ║") + "\n")

	// ── Row 4: divider. ─────────────────────────────────────────────────
	b.WriteString(chrome.Render("║ ") +
		frame.Render("┌"+strings.Repeat("─", inner-4)+"┐") +
		chrome.Render(" ║") + "\n")

	// ── Rows 5–6: key + scan bar window. ────────────────────────────────
	keyGlyph := v.keyArt(accent)
	scanLine := v.scanBar.ViewAs(v.scanPct)
	pctTxt := fmt.Sprintf("%3.0f%%", v.scanPct*100)

	// key art is one rune; pad and place it on the left
	keyTxt := fgBoldStyle(accent).Render(keyGlyph)
	scanContent := keyTxt + "  " + scanLine + "  " + steel.Render(pctTxt)
	b.WriteString(chrome.Render("║ ") +
		frame.Render("│") +
		" " + pad(scanContent, inner-6) + " " +
		frame.Render("│") +
		chrome.Render(" ║") + "\n")

	// Mode-specific second line: scanning shows hint, results show verdict pill
	var infoLine string
	switch v.mode {
	case vaultScanning:
		dots := strings.Repeat(".", (v.frame/5)%4)
		infoLine = steel.Render("fetching key from keyserver") + fgBoldStyle(colorAmber).Render(dots)
	case vaultVerified:
		infoLine = fgBoldStyle(colorMint).Render("⬢ keyring trust verified — payload unaltered")
	case vaultBreached:
		infoLine = fgBoldStyle(colorMagenta).Render("◈ verification failed — see fault detail below")
	}
	b.WriteString(chrome.Render("║ ") +
		frame.Render("│") +
		" " + pad(infoLine, inner-6) + " " +
		frame.Render("│") +
		chrome.Render(" ║") + "\n")

	// ── Row 7: bottom of inner panel. ───────────────────────────────────
	b.WriteString(chrome.Render("║ ") +
		frame.Render("└"+strings.Repeat("─", inner-4)+"┘") +
		chrome.Render(" ║") + "\n")

	// ── Rows 8–11: structured detail rows (always rendered; — for empty). ─
	rowFor := func(label, val string, valCol lipgloss.Color) {
		if val == "" {
			val = "—"
		}
		l := steel.Render(pad(label, 14))
		valSty := fgStyle(valCol)
		if valCol == colorFrost {
			valSty = frost
		}
		row := "  " + l + valSty.Render(truncate(val, inner-20))
		b.WriteString(chrome.Render("║ ") + pad(row, inner-2) + chrome.Render(" ║") + "\n")
	}
	rowFor("fingerprint", v.details.Fingerprint, colorPhosphor)
	rowFor("signed by", v.details.SignedBy, colorFrost)
	rowFor("key id", v.details.KeyID, colorAmber)
	rowFor("signed at", v.details.SignedAt, colorSteel)

	// ── Row 12: verdict strip. ──────────────────────────────────────────
	verdictRow := "  " + steel.Render(pad("verdict", 14)) +
		fgBoldStyle(verdictCol).Render(verdict)
	b.WriteString(chrome.Render("║ ") + pad(verdictRow, inner-2) + chrome.Render(" ║") + "\n")

	// ── Row 13: status caption. ─────────────────────────────────────────
	statusContent := steel.Render("STATUS: ") +
		fgBoldStyle(statusCol).Render(statusStr)
	b.WriteString(chrome.Render("║ ") + pad(statusContent, inner-2) + chrome.Render(" ║") + "\n")

	// ── Row 14: bottom rivet plate. ─────────────────────────────────────
	rivetCount := (inner - 2) / 2
	rivets := strings.Repeat(steel.Render("▪")+" ", rivetCount)
	b.WriteString(chrome.Render("║ ") + pad(rivets, inner-2) + chrome.Render(" ║") + "\n")

	// ── Row 15: bottom frame. ───────────────────────────────────────────
	half := (inner - 1) / 2
	b.WriteString(chrome.Render("╚") +
		chrome.Render(strings.Repeat("═", half)) +
		chrome.Render("┴") +
		chrome.Render(strings.Repeat("═", inner-half-1)) +
		chrome.Render("╝"))

	return b.String()
}

// keyArt returns a small rotating-key glyph that animates while scanning.
func (v verifyAnimation) keyArt(_ lipgloss.Color) string {
	switch v.mode {
	case vaultVerified:
		return "⬢"
	case vaultBreached:
		return "◈"
	}
	frames := []string{"⚷", "🔑", "⚿", "🗝"}
	return frames[v.keyFrame%len(frames)]
}

// ParseGPGOutput extracts structured fields from `gpg --verify` output so
// the vault's detail rows show signing identity instead of raw stderr.
//
// Recognised lines (gpg ≥ 2.2):
//
//	gpg: Signature made Mon Jan  1 12:00:00 2024 UTC
//	gpg:                using RSA key ABCDEF1234567890ABCDEF1234567890DEADBEEF
//	gpg: Good signature from "Linus Torvalds <torvalds@kernel.org>" [unknown]
//	Primary key fingerprint: ABCD 1234 EF56 7890 ABCD 1234 EF56 7890 ABCD 1234
//	gpg: BAD signature from "Mallory <mallory@example.com>"
func ParseGPGOutput(output string) VerifyDetails {
	d := VerifyDetails{Raw: output}

	// Signature timestamp
	if m := regexp.MustCompile(`Signature made\s+(.+)`).FindStringSubmatch(output); len(m) >= 2 {
		// gpg appends "  using RSA key …" sometimes on the same line — split.
		ts := strings.TrimSpace(m[1])
		if i := strings.Index(ts, "using "); i >= 0 {
			ts = strings.TrimSpace(ts[:i])
		}
		d.SignedAt = ts
	}

	// Long key id (40-hex preferred; 16-hex fallback)
	if m := regexp.MustCompile(`(?i)using\s+\w+\s+key\s+([0-9A-F]{16,40})`).FindStringSubmatch(output); len(m) >= 2 {
		d.KeyID = strings.ToUpper(m[1])
	}

	// Fingerprint — 40 hex chars, possibly with spaces every 4
	fpRe := regexp.MustCompile(`(?i)fingerprint[:\s]+([0-9A-F\s]{40,})`)
	if m := fpRe.FindStringSubmatch(output); len(m) >= 2 {
		raw := strings.ToUpper(strings.ReplaceAll(m[1], " ", ""))
		// keep only hex prefix
		hex := regexp.MustCompile(`[0-9A-F]+`).FindString(raw)
		if len(hex) >= 40 {
			hex = hex[:40]
			// re-space for readability: groups of 4, double-space mid
			d.Fingerprint = formatFingerprint(hex)
			if d.KeyID == "" {
				d.KeyID = hex[len(hex)-16:]
			}
		}
	}

	// Signing identity
	idRe := regexp.MustCompile(`(?i)signature from\s+"([^"]+)"`)
	if m := idRe.FindStringSubmatch(output); len(m) >= 2 {
		d.SignedBy = m[1]
	}

	// Verdict label
	switch {
	case regexp.MustCompile(`(?i)Good signature`).MatchString(output):
		d.Verdict = "GOOD SIGNATURE"
	case regexp.MustCompile(`(?i)BAD signature`).MatchString(output):
		d.Verdict = "BAD SIGNATURE"
	case regexp.MustCompile(`(?i)no public key|cannot find|missing`).MatchString(output):
		d.Verdict = "PUBLIC KEY MISSING"
	case regexp.MustCompile(`(?i)expired`).MatchString(output):
		d.Verdict = "KEY EXPIRED"
	}

	return d
}

// formatFingerprint inserts a space every 4 hex chars and a double-space
// at the midpoint, matching the canonical `gpg --fingerprint` layout.
func formatFingerprint(hex40 string) string {
	if len(hex40) != 40 {
		return hex40
	}
	groups := make([]string, 0, 10)
	for i := 0; i < 40; i += 4 {
		groups = append(groups, hex40[i:i+4])
	}
	return strings.Join(groups[:5], " ") + "  " + strings.Join(groups[5:], " ")
}
