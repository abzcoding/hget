package main

import (
	"net"
	"path/filepath"
	"testing"
)

func TestFilterIpV4(t *testing.T) {
	// Create a mix of IPv4 and IPv6 addresses
	ips := []net.IP{
		net.ParseIP("192.168.1.1"),
		net.ParseIP("fe80::1"),
		net.ParseIP("127.0.0.1"),
		net.ParseIP("::1"),
		net.ParseIP("10.0.0.1"),
	}

	// Filter IPv4 addresses
	filtered := FilterIPV4(ips)

	// Check if the filtered list contains exactly the 3 IPv4 addresses
	if len(filtered) != 3 {
		t.Fatalf("Expected 3 IPv4 addresses, got %d", len(filtered))
	}

	// Check if all filtered addresses are IPv4
	expectedAddrs := map[string]bool{
		"192.168.1.1": true,
		"127.0.0.1":   true,
		"10.0.0.1":    true,
	}

	for _, ip := range filtered {
		if !expectedAddrs[ip] {
			t.Fatalf("Unexpected IPv4 address: %s", ip)
		}
	}
}

func TestFolderOfPanic1(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("The code did not panic")
		}
	}()
	url := "http://foo.bar/.."
	FolderOf(url)
}

func TestFolderOfPanic2(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("The code did not panic")
		}
	}()
	url := "http://foo.bar/../../../foobar"
	FolderOf(url)
}

func TestFolderOfNormal(t *testing.T) {
	url := "http://foo.bar/file"
	u := FolderOf(url)
	if filepath.Base(u) != "file" {
		t.Fatalf("url of return incorrect value")
	}
}

func TestFolderWithoutParams(t *testing.T) {
	url := "http://foo.bar/file?param=value"
	u := FolderOf(url)
	if filepath.Base(u) != "file" {
		t.Fatalf("url of return incorrect value")
	}
}

func TestTaskFromURL(t *testing.T) {
	testCases := []struct {
		url      string
		expected string
	}{
		{"http://example.com/path/to/file.zip", "file.zip"},
		{"https://download.com/file.tar.gz?token=123", "file.tar.gz"},
		{"http://domain.com/path/", "path"},
		{"https://test.org/path/to/file.txt#fragment", "file.txt"},
	}

	for _, tc := range testCases {
		result := TaskFromURL(tc.url)
		if result != tc.expected {
			t.Errorf("TaskFromURL(%s) = %s; want %s", tc.url, result, tc.expected)
		}
	}
}

func TestIsURL(t *testing.T) {
	validURLs := []string{
		"http://example.com",
		"https://test.org/path",
		"ftp://files.org/file.zip",
		"http://localhost:8080",
	}

	invalidURLs := []string{
		"not a url",
		"http:/missing-slash",
		"://no-scheme",
	}

	for _, url := range validURLs {
		if !IsURL(url) {
			t.Errorf("IsURL(%s) = false; want true", url)
		}
	}

	for _, url := range invalidURLs {
		if IsURL(url) {
			t.Errorf("IsURL(%s) = true; want false", url)
		}
	}
}

func TestMkdirIfNotExist(t *testing.T) {
	// Test creating a temporary directory
	tempDir := filepath.Join(t.TempDir(), "test-dir")

	// Directory shouldn't exist yet
	if ExistDir(tempDir) {
		t.Fatalf("Directory %s should not exist yet", tempDir)
	}

	// Create the directory
	err := MkdirIfNotExist(tempDir)
	if err != nil {
		t.Fatalf("MkdirIfNotExist failed: %v", err)
	}

	// Directory should now exist
	if !ExistDir(tempDir) {
		t.Fatalf("Directory %s should exist after MkdirIfNotExist", tempDir)
	}

	// Running again on existing directory should not error
	err = MkdirIfNotExist(tempDir)
	if err != nil {
		t.Fatalf("MkdirIfNotExist on existing dir failed: %v", err)
	}
}
