package state

import (
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/abzcoding/hget/internal/util"
)

func TestFolderOfPanic1(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("The code did not panic")
		}
	}()
	FolderOf("http://foo.bar/..")
}

func TestFolderOfPanic2(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("The code did not panic")
		}
	}()
	FolderOf("http://foo.bar/../../../foobar")
}

func TestFolderOfNormal(t *testing.T) {
	url := "http://foo.bar/file"
	u := FolderOf(url)
	if filepath.Base(u) != "file" {
		t.Fatalf("FolderOf returned incorrect value")
	}
}

func TestFolderWithoutParams(t *testing.T) {
	url := "http://foo.bar/file?param=value"
	u := FolderOf(url)
	if filepath.Base(u) != "file" {
		t.Fatalf("FolderOf returned incorrect value")
	}
}

func TestStateSave(t *testing.T) {
	originalDataFolder := DataFolder
	DataFolder = ".hget_test/"
	defer func() {
		DataFolder = originalDataFolder
		usr, _ := user.Current()
		testFolder := filepath.Join(usr.HomeDir, DataFolder)
		os.RemoveAll(testFolder)
	}()

	testURL := "http://example.com/test.zip"
	s := &State{
		URL: testURL,
		Parts: []Part{
			{Index: 0, URL: testURL, Path: "temp_part0", RangeFrom: 0, RangeTo: 100},
			{Index: 1, URL: testURL, Path: "temp_part1", RangeFrom: 101, RangeTo: 200},
		},
	}

	for _, part := range s.Parts {
		err := os.WriteFile(part.Path, []byte("test content"), 0644)
		if err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
		defer os.Remove(part.Path)
	}

	err := s.Save()
	if err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	folder := FolderOf(testURL)
	stateFilePath := filepath.Join(folder, StateFileName)

	if _, err := os.Stat(stateFilePath); os.IsNotExist(err) {
		t.Fatalf("State file was not created at %s", stateFilePath)
	}

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

	for _, part := range s.Parts {
		movedPath := filepath.Join(folder, filepath.Base(part.Path))
		if _, err := os.Stat(movedPath); os.IsNotExist(err) {
			t.Errorf("Part file not moved to %s", movedPath)
		} else {
			os.Remove(movedPath)
		}
	}
}

func TestRead(t *testing.T) {
	originalDataFolder := DataFolder
	DataFolder = ".hget_test/"
	defer func() {
		DataFolder = originalDataFolder
		usr, _ := user.Current()
		testFolder := filepath.Join(usr.HomeDir, DataFolder)
		os.RemoveAll(testFolder)
	}()

	testURL := "http://example.com/test.zip"
	testState := &State{
		URL: testURL,
		Parts: []Part{
			{Index: 0, URL: testURL, Path: "part0", RangeFrom: 0, RangeTo: 100},
			{Index: 1, URL: testURL, Path: "part1", RangeFrom: 101, RangeTo: 200},
		},
	}

	usr, _ := user.Current()
	homeDir := usr.HomeDir
	taskName := util.TaskFromURL(testURL)
	folderPath := filepath.Join(homeDir, DataFolder, taskName)
	stateFilePath := filepath.Join(folderPath, StateFileName)

	err := os.MkdirAll(folderPath, 0755)
	if err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	stateData, err := json.Marshal(testState)
	if err != nil {
		t.Fatalf("Failed to marshal test state: %v", err)
	}

	err = os.WriteFile(stateFilePath, stateData, 0644)
	if err != nil {
		t.Fatalf("Failed to write test state file: %v", err)
	}

	st, err := Read(testURL)
	if err != nil {
		t.Fatalf("Read() failed: %v", err)
	}

	if st.URL != testState.URL {
		t.Errorf("Expected URL %s, got %s", testState.URL, st.URL)
	}

	if len(st.Parts) != len(testState.Parts) {
		t.Errorf("Expected %d parts, got %d", len(testState.Parts), len(st.Parts))
	}

	for i, part := range st.Parts {
		if part.Index != testState.Parts[i].Index ||
			part.URL != testState.Parts[i].URL ||
			part.RangeFrom != testState.Parts[i].RangeFrom ||
			part.RangeTo != testState.Parts[i].RangeTo {
			t.Errorf("Part %d does not match expected values", i)
		}
	}
}

func TestReadNonExistent(t *testing.T) {
	_, err := Read("http://nonexistent.example.com/file.zip")
	if err == nil {
		t.Errorf("Expected error when reading non-existent state, got nil")
	}
}
