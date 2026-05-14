package extractor

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestOptions_AuthArgs_Empty(t *testing.T) {
	if got := (Options{}).authArgs(); len(got) != 0 {
		t.Errorf("zero Options should produce no args, got %v", got)
	}
}

func TestOptions_AuthArgs_CookiesFile(t *testing.T) {
	got := Options{CookiesFile: "/tmp/cookies.txt"}.authArgs()
	want := []string{"--cookies", "/tmp/cookies.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CookiesFile authArgs=%v want %v", got, want)
	}
}

func TestOptions_AuthArgs_CookiesFromBrowser(t *testing.T) {
	got := Options{CookiesFromBrowser: "firefox:Default"}.authArgs()
	want := []string{"--cookies-from-browser", "firefox:Default"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CookiesFromBrowser authArgs=%v want %v", got, want)
	}
}

func TestOptions_AuthArgs_Both(t *testing.T) {
	// When both are set we forward both — yt-dlp itself decides
	// precedence (latest winning).  This keeps the wrapper agnostic.
	got := Options{
		CookiesFile:        "/tmp/c.txt",
		CookiesFromBrowser: "chrome",
	}.authArgs()
	want := []string{
		"--cookies", "/tmp/c.txt",
		"--cookies-from-browser", "chrome",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("authArgs=%v want %v", got, want)
	}
}

// TestRun_ForwardsCookieFlagsToYTDLP boots a shim that records its argv
// and verifies the cookie flags actually land on the yt-dlp command-line.
// Without this test, a refactor of args[] could silently drop them.
func TestRun_ForwardsCookieFlagsToYTDLP(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shim is POSIX-only")
	}
	tmp := t.TempDir()
	shim := filepath.Join(tmp, "yt-dlp")
	argDump := filepath.Join(tmp, "argv")
	script := `#!/bin/sh
printf '%s\n' "$@" > ` + argDump + `
echo "[download] Destination: /tmp/out.mp4"
echo "HGET|100.0%|10|10|10|0|NA|NA"
exit 0
`
	if err := os.WriteFile(shim, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmp)

	sink := &fakeSink{}
	_, err := Run(context.Background(), "https://example.com/x", "",
		Options{CookiesFile: "/tmp/c.txt", CookiesFromBrowser: "firefox"},
		FormatSelection{},
		sink)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	raw, err := os.ReadFile(argDump)
	if err != nil {
		t.Fatalf("read argv dump: %v", err)
	}
	args := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	mustHaveSeq(t, args, "--cookies", "/tmp/c.txt")
	mustHaveSeq(t, args, "--cookies-from-browser", "firefox")
}

// TestProbe_ForwardsCookieFlagsToYTDLP — same idea, but for the JSON
// probe path.  YouTube's bot challenge gates the probe too, so cookies
// must reach yt-dlp -J as well.
func TestProbe_ForwardsCookieFlagsToYTDLP(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shim is POSIX-only")
	}
	tmp := t.TempDir()
	shim := filepath.Join(tmp, "yt-dlp")
	argDump := filepath.Join(tmp, "argv")
	// shim must emit a valid -J JSON document so parseMetaJSON succeeds.
	script := `#!/bin/sh
printf '%s\n' "$@" > ` + argDump + `
echo '{"title":"Probe Sample","ext":"mp4","duration":10}'
exit 0
`
	if err := os.WriteFile(shim, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmp)

	_, err := Probe(context.Background(), "https://example.com/x",
		Options{CookiesFile: "/tmp/c.txt"})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	raw, err := os.ReadFile(argDump)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	mustHaveSeq(t, args, "--cookies", "/tmp/c.txt")
	// -J still present?
	mustContainArg(t, args, "-J")
}

// mustHaveSeq fails the test unless `seq` appears as consecutive
// elements in args.
func mustHaveSeq(t *testing.T, args []string, seq ...string) {
	t.Helper()
	for i := 0; i+len(seq) <= len(args); i++ {
		match := true
		for j, s := range seq {
			if args[i+j] != s {
				match = false
				break
			}
		}
		if match {
			return
		}
	}
	t.Errorf("expected sequence %v in args; got %v", seq, args)
}

func mustContainArg(t *testing.T, args []string, want string) {
	t.Helper()
	for _, a := range args {
		if a == want {
			return
		}
	}
	t.Errorf("expected arg %q in args; got %v", want, args)
}
