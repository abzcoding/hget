package main

import (
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"testing"
)

func TestStateSave(t *testing.T) {
	// Setup test environment
	originalDataFolder := dataFolder
	dataFolder = ".hget_test/"
	defer func() {
		dataFolder = originalDataFolder
		usr, _ := user.Current()
		testFolder := filepath.Join(usr.HomeDir, dataFolder)
		os.RemoveAll(testFolder)
	}()

	// Create test state
	testURL := "http://example.com/test.zip"
	s := &State{
		URL: testURL,
		Parts: []Part{
			{
				Index:     0,
				URL:       testURL,
				Path:      "temp_part0",
				RangeFrom: 0,
				RangeTo:   100,
			},
			{
				Index:     1,
				URL:       testURL,
				Path:      "temp_part1",
				RangeFrom: 101,
				RangeTo:   200,
			},
		},
	}

	// Create temporary files for parts
	for _, part := range s.Parts {
		err := os.WriteFile(part.Path, []byte("test content"), 0644)
		if err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
		defer os.Remove(part.Path) // Cleanup in case the test fails
	}

	// Test Save method
	err := s.Save()
	if err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	// Verify state file was created
	folder := FolderOf(testURL)
	stateFilePath := filepath.Join(folder, stateFileName)

	if _, err := os.Stat(stateFilePath); os.IsNotExist(err) {
		t.Fatalf("State file was not created at %s", stateFilePath)
	}

	// Verify content of the state file
	stateBytes, err := os.ReadFile(stateFilePath)
	if err != nil {
		t.Fatalf("Could not read state file: %v", err)
	}

	var savedState State
	err = json.Unmarshal(stateBytes, &savedState)
	if err != nil {
		t.Fatalf("Could not unmarshal state file: %v", err)
	}

	if savedState.URL != testURL {
		t.Errorf("Expected URL %s, got %s", testURL, savedState.URL)
	}

	if len(savedState.Parts) != len(s.Parts) {
		t.Errorf("Expected %d parts, got %d", len(s.Parts), len(savedState.Parts))
	}

	// Verify part files were moved
	for _, part := range s.Parts {
		movedPath := filepath.Join(folder, filepath.Base(part.Path))
		if _, err := os.Stat(movedPath); os.IsNotExist(err) {
			t.Errorf("Part file not moved to %s", movedPath)
		} else {
			// Clean up moved files
			os.Remove(movedPath)
		}
	}
}

func TestRead(t *testing.T) {
	// Setup test environment
	originalDataFolder := dataFolder
	dataFolder = ".hget_test/"
	defer func() {
		dataFolder = originalDataFolder
		usr, _ := user.Current()
		testFolder := filepath.Join(usr.HomeDir, dataFolder)
		os.RemoveAll(testFolder)
	}()

	// Create test data
	testURL := "http://example.com/test.zip"
	testState := &State{
		URL: testURL,
		Parts: []Part{
			{
				Index:     0,
				URL:       testURL,
				Path:      "part0",
				RangeFrom: 0,
				RangeTo:   100,
			},
			{
				Index:     1,
				URL:       testURL,
				Path:      "part1",
				RangeFrom: 101,
				RangeTo:   200,
			},
		},
	}

	// Set up directory structure
	usr, _ := user.Current()
	homeDir := usr.HomeDir
	taskName := TaskFromURL(testURL)
	folderPath := filepath.Join(homeDir, dataFolder, taskName)
	stateFilePath := filepath.Join(folderPath, stateFileName)

	// Create directory
	err := os.MkdirAll(folderPath, 0755)
	if err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	// Write test state file
	stateData, err := json.Marshal(testState)
	if err != nil {
		t.Fatalf("Failed to marshal test state: %v", err)
	}

	err = os.WriteFile(stateFilePath, stateData, 0644)
	if err != nil {
		t.Fatalf("Failed to write test state file: %v", err)
	}

	// Test Read function
	state, err := Read(testURL)
	if err != nil {
		t.Fatalf("Read() failed: %v", err)
	}

	// Verify the read state matches the test state
	if state.URL != testState.URL {
		t.Errorf("Expected URL %s, got %s", testState.URL, state.URL)
	}

	if len(state.Parts) != len(testState.Parts) {
		t.Errorf("Expected %d parts, got %d", len(testState.Parts), len(state.Parts))
	}

	for i, part := range state.Parts {
		if part.Index != testState.Parts[i].Index ||
			part.URL != testState.Parts[i].URL ||
			part.RangeFrom != testState.Parts[i].RangeFrom ||
			part.RangeTo != testState.Parts[i].RangeTo {
			t.Errorf("Part %d does not match expected values", i)
		}
	}
}

func TestReadNonExistent(t *testing.T) {
	// Test reading a non-existent state file
	_, err := Read("http://nonexistent.example.com/file.zip")
	if err == nil {
		t.Errorf("Expected error when reading non-existent state, got nil")
	}
}
