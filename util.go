package main

import (
	"errors"
	"net"
	"net/url"
	"os"
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
	safePath := filepath.Join(os.Getenv("HOME"), dataFolder)

	// Extract the last path from the URL, excluding parameters.
	// eg: URL_ADDRESS.com/path/to/file?param=value -> file
	cleanPath := TaskFromURL(urlStr)

	fullQualifyPath, err := filepath.Abs(filepath.Join(os.Getenv("HOME"), dataFolder, cleanPath))
	FatalCheck(err)

	//must ensure full qualify path is CHILD of safe path
	//to prevent directory traversal attack
	//using Rel function to get relative between parent and child
	//if relative join base == child, then child path MUST BE real child
	relative, err := filepath.Rel(safePath, fullQualifyPath)
	FatalCheck(err)

	if strings.Contains(relative, "..") {
		FatalCheck(errors.New("you may be a victim of directory traversal path attack"))
		return "" //return is redundant be cause in fatal check we have panic, but compiler does not able to check
	}
	return fullQualifyPath

}

// TaskFromURL runs when you want to download a single url
func TaskFromURL(urlStr string) string {
	// Extract the last path from the URL, excluding parameters.
	// eg: URL_ADDRESS.com/path/to/file?param=value -> file
	parsedURL, err := url.Parse(urlStr)
	FatalCheck(err)
	return filepath.Base(strings.TrimRight(parsedURL.Path, "/\\"))
}

// IsURL checks if `s` is actually a parsable URL.
func IsURL(s string) bool {
	_, err := url.Parse(s)
	return err == nil
}
