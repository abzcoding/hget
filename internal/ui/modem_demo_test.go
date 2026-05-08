package ui

import (
	"fmt"
	"testing"
)

// TestModemHandshakeAnimation demonstrates the modem handshake phases.
func TestModemHandshakeAnimation(t *testing.T) {
	if testing.Short() {
		t.Skip("visual demo")
	}

	modem := newModemHandshake()
	url := "https://example.com/large-file.iso"

	phases := []struct {
		name   string
		frames int
	}{
		{"DIALING", 15},
		{"CARRIER DETECT", 15},
		{"HANDSHAKE", 15},
		{"LINK ESTABLISHED", 10},
	}

	for _, phase := range phases {
		fmt.Printf("\n─── %s ───\n", phase.name)
		for i := 0; i < phase.frames; i++ {
			modem.Tick()
		}
		fmt.Println(modem.View(url))
	}
}

// TestSkipAnimation demonstrates red blinking LEDs during skip.
func TestSkipAnimation(t *testing.T) {
	if testing.Short() {
		t.Skip("visual demo")
	}

	d := newDataLink()

	channels := []channelRow{
		{Index: 1, Pct: 0.45, RawPct: 0.45, Speed: 0, HasStarted: true},
		{Index: 2, Pct: 0.32, RawPct: 0.32, Speed: 0, HasStarted: true},
		{Index: 3, Pct: 0.51, RawPct: 0.51, Speed: 0, HasStarted: true},
		{Index: 4, Pct: 0.38, RawPct: 0.38, Speed: 0, HasStarted: true},
	}

	fmt.Println("\n─── SKIPPING (red blinking LEDs) ───")

	// Tick a few times to show the blinking animation
	for i := 0; i < 8; i++ {
		d.Tick(0, 5_900_000, []float64{0, 0, 0, 0})
	}

	fmt.Println(d.View(channels, 45_000_000, 100_000_000, 5_900_000, 0, "SKIPPING", false))
}

// TestJoinAnimation demonstrates the file assembly animation.
func TestJoinAnimation(t *testing.T) {
	if testing.Short() {
		t.Skip("visual demo")
	}

	join := newJoinAnimation()

	stages := []struct {
		name    string
		pct     float64
		current int
		total   int
		frames  int
	}{
		{"Starting", 0.1, 1, 8, 5},
		{"Mid-assembly", 0.5, 4, 8, 5},
		{"Almost done", 0.9, 7, 8, 5},
		{"Complete", 1.0, 8, 8, 5},
	}

	for _, stage := range stages {
		fmt.Printf("\n─── %s ───\n", stage.name)
		for i := 0; i < stage.frames; i++ {
			join.Tick()
		}
		fmt.Println(join.View(stage.pct, stage.current, stage.total))
	}
}

// TestVerifyAnimation demonstrates the GPG verification vault panel
// across all three operational modes (scanning, verified, breached).
func TestVerifyAnimation(t *testing.T) {
	if testing.Short() {
		t.Skip("visual demo")
	}

	// ── Scanning mode (animated) ──────────────────────────────────────────
	scan := newVerifyAnimation()
	fmt.Println("\n─── Vault: scanning ───")
	for i := 0; i < 25; i++ {
		scan.Tick()
	}
	fmt.Println(scan.View())

	// ── Verified mode (parsed details) ────────────────────────────────────
	good := newVerifyAnimation()
	for i := 0; i < 30; i++ {
		good.Tick()
	}
	good.SetVerified(ParseGPGOutput(`gpg: Signature made Mon Jan  1 12:00:00 2024 UTC
gpg:                using RSA key ABCDEF1234567890ABCDEF1234567890DEADBEEF
gpg: Good signature from "Linus Torvalds <torvalds@kernel.org>" [unknown]
Primary key fingerprint: ABCD 1234 EF56 7890 ABCD  1234 EF56 7890 DEAD BEEF`))
	for i := 0; i < 5; i++ {
		good.Tick()
	}
	fmt.Println("\n─── Vault: verified ───")
	fmt.Println(good.View())

	// ── Breached mode ─────────────────────────────────────────────────────
	bad := newVerifyAnimation()
	bad.SetBreached(ParseGPGOutput(`gpg: Signature made Mon Jan  1 12:00:00 2024 UTC
gpg:                using RSA key DEADBEEFCAFEBABE
gpg: BAD signature from "Mallory <mallory@example.com>"`))
	for i := 0; i < 5; i++ {
		bad.Tick()
	}
	fmt.Println("\n─── Vault: breached ───")
	fmt.Println(bad.View())
}

// TestParseGPGOutput exercises the GPG-output parser for a few common
// shapes so the vault panel can rely on structured fields.
func TestParseGPGOutput(t *testing.T) {
	good := `gpg: Signature made Mon Jan 01 12:00:00 2024 UTC
gpg:                using RSA key 0123456789ABCDEF0123456789ABCDEF01234567
gpg: Good signature from "Alice <alice@example.com>" [unknown]
Primary key fingerprint: 0123 4567 89AB CDEF 0123  4567 89AB CDEF 0123 4567`
	d := ParseGPGOutput(good)
	if d.SignedBy != "Alice <alice@example.com>" {
		t.Errorf("SignedBy = %q", d.SignedBy)
	}
	if d.Verdict != "GOOD SIGNATURE" {
		t.Errorf("Verdict = %q", d.Verdict)
	}
	if d.KeyID == "" {
		t.Errorf("KeyID empty")
	}
	if d.Fingerprint == "" {
		t.Errorf("Fingerprint empty")
	}
	if d.SignedAt == "" {
		t.Errorf("SignedAt empty")
	}

	bad := `gpg: BAD signature from "Eve"`
	if got := ParseGPGOutput(bad).Verdict; got != "BAD SIGNATURE" {
		t.Errorf("bad verdict = %q", got)
	}

	missing := `gpg: Can't check signature: No public key`
	if got := ParseGPGOutput(missing).Verdict; got != "PUBLIC KEY MISSING" {
		t.Errorf("missing verdict = %q", got)
	}
}
