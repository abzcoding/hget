package downloader

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// DownloadSigFile fetches the detached GPG signature file at sigURL and writes
// it to destPath.  It reuses the same proxy/TLS settings as the main download.
func DownloadSigFile(sigURL, destPath string, skipTLS bool, proxyServer string, timeout time.Duration) error {
	client := ProxyAwareHTTPClient(proxyServer, skipTLS, timeout)

	req, err := http.NewRequest(http.MethodGet, sigURL, nil)
	if err != nil {
		return fmt.Errorf("building sig request: %w", err)
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Accept-Encoding", "identity")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("fetching sig file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sig file download returned HTTP %d for %s", resp.StatusCode, sigURL)
	}

	f, err := os.Create(destPath) // #nosec G304 – path is derived from the user-supplied URL
	if err != nil {
		return fmt.Errorf("creating sig file: %w", err)
	}
	defer f.Close()

	if _, err = io.Copy(f, resp.Body); err != nil {
		return fmt.Errorf("writing sig file: %w", err)
	}
	return nil
}

// VerifyGPGSignature runs `gpg --verify sigPath filePath` and returns an error
// (including gpg's stderr output) when verification fails.
//
// It passes --keyserver-options auto-key-retrieve so missing public keys are
// fetched automatically from the default keyserver.  When the key still cannot
// be found (e.g. the keyserver is unreachable), the key fingerprint parsed from
// gpg output is included in the error so the caller can surface a manual hint.
func VerifyGPGSignature(sigPath, filePath string) (string, error) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		gpgBin, err = exec.LookPath("gpg2")
		if err != nil {
			return "", fmt.Errorf("gpg not found in PATH – please install GnuPG")
		}
	}

	// #nosec G204 – sigPath and filePath are derived from user-supplied URL and cwd
	cmd := exec.Command(gpgBin,
		"--keyserver-options", "auto-key-retrieve",
		"--verify", sigPath, filePath,
	)
	out, err := cmd.CombinedOutput()
	detail := strings.TrimSpace(string(out))
	if err != nil {
		// If the key is simply missing, extract the fingerprint and give a hint.
		if strings.Contains(detail, "No public key") || strings.Contains(detail, "no public key") {
			fp := extractKeyFingerprint(detail)
			hint := "gpg verification failed: public key not in keyring"
			if fp != "" {
				hint += fmt.Sprintf("\n  Import it manually:  gpg --recv-keys %s", fp)
			}
			return detail, fmt.Errorf("%s", hint)
		}
		return detail, fmt.Errorf("gpg verification failed: %s", detail)
	}
	return detail, nil
}

// extractKeyFingerprint parses a 40-hex-character key fingerprint from gpg output.
var reFP = regexp.MustCompile(`(?i)\b([0-9A-F]{40})\b`)

func extractKeyFingerprint(output string) string {
	m := reFP.FindStringSubmatch(output)
	if len(m) >= 2 {
		return m[1]
	}
	// Fallback: look for shorter key-ID lines like "key XXXXXXXXXXXXXXXX:"
	reID := regexp.MustCompile(`(?i)key\s+([0-9A-F]{16})`)
	m2 := reID.FindStringSubmatch(output)
	if len(m2) >= 2 {
		return m2[1]
	}
	return ""
}
