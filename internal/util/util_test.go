package util

import (
	"net"
	"path/filepath"
	"testing"
)

func TestFilterIpV4(t *testing.T) {
	ips := []net.IP{
		net.ParseIP("192.168.1.1"),
		net.ParseIP("fe80::1"),
		net.ParseIP("127.0.0.1"),
		net.ParseIP("::1"),
		net.ParseIP("10.0.0.1"),
	}

	filtered := FilterIPV4(ips)

	if len(filtered) != 3 {
		t.Fatalf("Expected 3 IPv4 addresses, got %d", len(filtered))
	}

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
	tempDir := filepath.Join(t.TempDir(), "test-dir")

	if ExistDir(tempDir) {
		t.Fatalf("Directory %s should not exist yet", tempDir)
	}

	err := MkdirIfNotExist(tempDir)
	if err != nil {
		t.Fatalf("MkdirIfNotExist failed: %v", err)
	}

	if !ExistDir(tempDir) {
		t.Fatalf("Directory %s should exist after MkdirIfNotExist", tempDir)
	}

	err = MkdirIfNotExist(tempDir)
	if err != nil {
		t.Fatalf("MkdirIfNotExist on existing dir failed: %v", err)
	}
}
