package state

import (
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/abzcoding/hget/internal/util"
)

func prepareResume(t *testing.T, url string, parts []Part) (string, string) {
	t.Helper()
	usr, _ := user.Current()
	homeDir := usr.HomeDir
	taskName := util.TaskFromURL(url)
	folderPath := filepath.Join(homeDir, DataFolder, taskName)
	stateFilePath := filepath.Join(folderPath, StateFileName)

	err := os.MkdirAll(folderPath, 0755)
	if err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	st := &State{
		URL:   url,
		Parts: parts,
	}

	stateData, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("Failed to marshal test state: %v", err)
	}

	err = os.WriteFile(stateFilePath, stateData, 0644)
	if err != nil {
		t.Fatalf("Failed to write test state file: %v", err)
	}

	for _, part := range parts {
		partPath := filepath.Join(folderPath, filepath.Base(part.Path))
		contentSize := part.RangeFrom / 2
		if contentSize == 0 {
			contentSize = 10
		}
		content := make([]byte, contentSize)
		err = os.WriteFile(partPath, content, 0644)
		if err != nil {
			t.Fatalf("Failed to create test part file: %v", err)
		}
	}

	return folderPath, taskName
}

func TestTaskPrint(t *testing.T) {
	originalDataFolder := DataFolder
	DataFolder = ".hget_test/"
	defer func() {
		DataFolder = originalDataFolder
		usr, _ := user.Current()
		testFolder := filepath.Join(usr.HomeDir, DataFolder)
		os.RemoveAll(testFolder)
	}()

	usr, _ := user.Current()
	homeDir := usr.HomeDir
	testFolder := filepath.Join(homeDir, DataFolder)

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

	testFile := filepath.Join(testFolder, "testfile.txt")
	err = os.WriteFile(testFile, []byte("test content"), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	err = TaskPrint()
	if err != nil {
		t.Fatalf("TaskPrint() failed: %v", err)
	}
}

func TestResumeNonExistent(t *testing.T) {
	originalDataFolder := DataFolder
	DataFolder = ".hget_test/"
	defer func() {
		DataFolder = originalDataFolder
		usr, _ := user.Current()
		testFolder := filepath.Join(usr.HomeDir, DataFolder)
		os.RemoveAll(testFolder)
	}()

	_, err := Resume("nonexistent-task")
	if err == nil {
		t.Errorf("Expected error when resuming non-existent task, got nil")
	}
}
