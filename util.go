package main

import (
	"errors"
	"net"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/mattn/go-isatty"
)

// FatalCheck panics if err is not nil.
func FatalCheck(err error) {
	if err != nil {
		Errorf("%v", err)
		panic(err)
	}
}

// FilterIPV4 returns parsed ipv4 string.
func FilterIPV4(ips []net.IP) []string {
	var ret = make([]string, 0)
	for _, ip := range ips {
		if ip.To4() != nil {
			ret = append(ret, ip.String())
		}
	}
	return ret
}

// MkdirIfNotExist creates `folder` directory if not available
func MkdirIfNotExist(folder string) error {
	if _, err := os.Stat(folder); err != nil {
		if err = os.MkdirAll(folder, 0700); err != nil {
			return err
		}
	}
	return nil
}

// ExistDir checks if `folder` is available
func ExistDir(folder string) bool {
	_, err := os.Stat(folder)
	return err == nil
}

// DisplayProgressBar shows a fancy progress bar
func DisplayProgressBar() bool {
	return isatty.IsTerminal(os.Stdout.Fd()) && displayProgress
}

// FolderOf makes sure you won't get LFI
func FolderOf(urlStr string) string {
	usr, err := user.Current()
	FatalCheck(err)
	homeDir := usr.HomeDir
	safePath := filepath.Join(homeDir, dataFolder)

	// Parse the URL to get the path
	parsedURL, err := url.Parse(urlStr)
	FatalCheck(err)

	// Check for directory traversal attempts in the raw path before cleaning
	if strings.Contains(parsedURL.Path, "..") {
		FatalCheck(errors.New("you may be a victim of directory traversal path attack"))
		return "" // Return is redundant because FatalCheck will panic
	}

	// Extract the last path from the URL, excluding parameters
	cleanPath := TaskFromURL(urlStr)

	fullQualifyPath, err := filepath.Abs(filepath.Join(homeDir, dataFolder, cleanPath))
	FatalCheck(err)

	// Double-check to ensure full qualify path is CHILD of safe path
	// to prevent directory traversal attack
	relative, err := filepath.Rel(safePath, fullQualifyPath)
	FatalCheck(err)

	if strings.Contains(relative, "..") {
		FatalCheck(errors.New("you may be a victim of directory traversal path attack"))
		return "" // Return is redundant because FatalCheck will panic
	}
	return fullQualifyPath
}

// TaskFromURL runs when you want to download a single url
func TaskFromURL(urlStr string) string {
	// Extract the last path from the URL, excluding parameters.
	// eg: URL_ADDRESS.com/path/to/file?param=value -> file
	parsedURL, err := url.Parse(urlStr)
	FatalCheck(err)
	// Clean the path to remove any directory traversal attempts
	// This ensures we only get the filename without any path manipulation
	cleanPath := filepath.Clean(parsedURL.Path)
	return filepath.Base(strings.TrimRight(cleanPath, "/\\"))
}

// IsURL checks if `s` is actually a parsable URL.
func IsURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	// Check for scheme and host to ensure it's a valid URL
	return u.Scheme != "" && u.Host != ""
}
