package util

import (
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// FatalCheck panics if err is not nil.
func FatalCheck(err error) {
	if err != nil {
		panic(err)
	}
}

// FilterIPV4 returns parsed ipv4 string.
func FilterIPV4(ips []net.IP) []string {
	ret := make([]string, 0)
	for _, ip := range ips {
		if ip.To4() != nil {
			ret = append(ret, ip.String())
		}
	}
	return ret
}

// MkdirIfNotExist creates `folder` if it doesn't already exist.
//
// MkdirAll is idempotent and atomic, so we don't pre-stat — pre-stating opens
// a TOCTOU window where a different process can create or replace the
// directory between the check and the call.
func MkdirIfNotExist(folder string) error {
	return os.MkdirAll(folder, 0700)
}

// ExistDir checks if `folder` is available.
func ExistDir(folder string) bool {
	_, err := os.Stat(folder)
	return err == nil
}

// TaskFromURL extracts the filename from a URL.
// e.g. http://example.com/path/to/file?param=value -> file
//
// On parse failure (rare — url.Parse is very permissive) it falls back to the
// basename of the raw input rather than panicking, so a single malformed entry
// in a batch URL list cannot crash the whole process.
func TaskFromURL(urlStr string) string {
	parsedURL, err := url.Parse(urlStr)
	var rawPath string
	if err != nil || parsedURL == nil {
		rawPath = urlStr
	} else {
		rawPath = parsedURL.Path
	}
	cleanPath := filepath.Clean(rawPath)
	base := filepath.Base(strings.TrimRight(cleanPath, "/\\"))
	if base == "." || base == "/" || base == "\\" || base == "" {
		return "download"
	}
	return base
}

// IsURL checks if `s` is actually a parsable URL.
func IsURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	return u.Scheme != "" && u.Host != ""
}

// SafeFolderPath builds and validates a download folder path, guarding against
// directory traversal. dataFolder must be a relative path like ".hget/".
func SafeFolderPath(homeDir, dataFolder, urlStr string) string {
	safePath := filepath.Join(homeDir, dataFolder)

	parsedURL, err := url.Parse(urlStr)
	FatalCheck(err)

	if strings.Contains(parsedURL.Path, "..") {
		FatalCheck(errors.New("you may be a victim of directory traversal path attack"))
		return ""
	}

	cleanPath := TaskFromURL(urlStr)

	fullQualifyPath, err := filepath.Abs(filepath.Join(homeDir, dataFolder, cleanPath))
	FatalCheck(err)

	if !strings.HasPrefix(fullQualifyPath, safePath) {
		FatalCheck(errors.New("path traversal attempt detected"))
	}

	return fullQualifyPath
}
