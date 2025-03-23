package main

import (
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"testing"
)

// Test setup and cleanup helpers
func prepareResume(t *testing.T, url string, parts []Part) (string, string) {
	// Create a temporary state file and part files for testing resume
	usr, _ := user.Current()
	homeDir := usr.HomeDir
	taskName := TaskFromURL(url)
	folderPath := filepath.Join(homeDir, dataFolder, taskName)
	stateFilePath := filepath.Join(folderPath, stateFileName)

	// Create the folder
	err := os.MkdirAll(folderPath, 0755)
	if err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	// Create the state file
	state := &State{
		URL:   url,
		Parts: parts,
	}

	stateData, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Failed to marshal test state: %v", err)
	}

	err = os.WriteFile(stateFilePath, stateData, 0644)
	if err != nil {
		t.Fatalf("Failed to write test state file: %v", err)
	}

	// Create part files with some content to simulate partial downloads
	for _, part := range parts {
		partPath := filepath.Join(folderPath, filepath.Base(part.Path))
		// Write different amounts of data to each part file to test resuming
		contentSize := part.RangeFrom / 2 // Arbitrary formula for test data size
		if contentSize == 0 {
			contentSize = 10 // Minimum size for test
		}
		content := make([]byte, contentSize)
		err = os.WriteFile(partPath, content, 0644)
		if err != nil {
			t.Fatalf("Failed to create test part file: %v", err)
		}
	}

	return folderPath, taskName
}

func cleanupResume(folderPath string) {
	os.RemoveAll(folderPath)
}

func TestTaskPrint(t *testing.T) {
	// Setup test environment
	originalDataFolder := dataFolder
	dataFolder = ".hget_test/"
	defer func() {
		dataFolder = originalDataFolder
		usr, _ := user.Current()
		testFolder := filepath.Join(usr.HomeDir, dataFolder)
		os.RemoveAll(testFolder)
	}()

	// Create a few test download directories
	usr, _ := user.Current()
	homeDir := usr.HomeDir
	testFolder := filepath.Join(homeDir, dataFolder)

	err := os.MkdirAll(testFolder, 0755)
	if err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	testDirs := []string{"test1", "test2", "test3"}
	for _, dir := range testDirs {
		err := os.MkdirAll(filepath.Join(testFolder, dir), 0755)
		if err != nil {
			t.Fatalf("Failed to create test subdirectory: %v", err)
		}
	}

	// Create a file too (should be ignored by TaskPrint)
	testFile := filepath.Join(testFolder, "testfile.txt")
	err = os.WriteFile(testFile, []byte("test content"), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Test TaskPrint
	err = TaskPrint()
	if err != nil {
		t.Fatalf("TaskPrint() failed: %v", err)
	}

	// Note: We can't easily check stdout, but we've verified the function executes without error
}

func TestResumeNonExistent(t *testing.T) {
	// Setup test environment
	originalDataFolder := dataFolder
	dataFolder = ".hget_test/"
	defer func() {
		dataFolder = originalDataFolder
		usr, _ := user.Current()
		testFolder := filepath.Join(usr.HomeDir, dataFolder)
		os.RemoveAll(testFolder)
	}()

	// Test resuming a non-existent task
	_, err := Resume("nonexistent-task")
	if err == nil {
		t.Errorf("Expected error when resuming non-existent task, got nil")
	}
}

// Go 1.21 has min function built-in, but for compatibility with older Go versions
func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
