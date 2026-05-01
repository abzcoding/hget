package joiner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/abzcoding/hget/internal/ui"
)

func TestJoiner(t *testing.T) {
	ui.DisplayProgress = false

	tempDir := t.TempDir()

	file1Path := filepath.Join(tempDir, "file1")
	file2Path := filepath.Join(tempDir, "file2")
	file3Path := filepath.Join(tempDir, "file3")
	joinedPath := filepath.Join(tempDir, "joined")

	file1Content := "content of file 1"
	file2Content := "content of file 2"
	file3Content := "content of file 3"

	if err := os.WriteFile(file1Path, []byte(file1Content), 0644); err != nil {
		t.Fatalf("Failed to create test file1: %v", err)
	}
	if err := os.WriteFile(file2Path, []byte(file2Content), 0644); err != nil {
		t.Fatalf("Failed to create test file2: %v", err)
	}
	if err := os.WriteFile(file3Path, []byte(file3Content), 0644); err != nil {
		t.Fatalf("Failed to create test file3: %v", err)
	}

	files := []string{file1Path, file2Path, file3Path}
	err := JoinFile(files, joinedPath)
	if err != nil {
		t.Fatalf("JoinFile() failed: %v", err)
	}

	joinedContent, err := os.ReadFile(joinedPath)
	if err != nil {
		t.Fatalf("Failed to read joined file: %v", err)
	}

	expectedContent := file1Content + file2Content + file3Content
	if string(joinedContent) != expectedContent {
		t.Errorf("Expected joined content '%s', got '%s'", expectedContent, string(joinedContent))
	}
}

func TestJoinerSorting(t *testing.T) {
	ui.DisplayProgress = false

	tempDir := t.TempDir()

	fileAPath := filepath.Join(tempDir, "fileC") // Intentionally out of order
	fileBPath := filepath.Join(tempDir, "fileA")
	fileCPath := filepath.Join(tempDir, "fileB")
	joinedPath := filepath.Join(tempDir, "joined")

	if err := os.WriteFile(fileAPath, []byte("C"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	if err := os.WriteFile(fileBPath, []byte("A"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	if err := os.WriteFile(fileCPath, []byte("B"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	files := []string{fileAPath, fileBPath, fileCPath}
	err := JoinFile(files, joinedPath)
	if err != nil {
		t.Fatalf("JoinFile() failed: %v", err)
	}

	joinedContent, err := os.ReadFile(joinedPath)
	if err != nil {
		t.Fatalf("Failed to read joined file: %v", err)
	}

	// Expect files to be joined in alphabetical order (A, B, C)
	expectedContent := "ABC"
	if string(joinedContent) != expectedContent {
		t.Errorf("Expected joined content '%s', got '%s' (files not sorted correctly)",
			expectedContent, string(joinedContent))
	}
}

func TestJoinerError(t *testing.T) {
	ui.DisplayProgress = false

	tempDir := t.TempDir()

	filePath := filepath.Join(tempDir, "file1")
	if err := os.WriteFile(filePath, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	nonExistentFile := filepath.Join(tempDir, "nonexistent")
	joinedPath := filepath.Join(tempDir, "joined")

	files := []string{filePath, nonExistentFile}
	err := JoinFile(files, joinedPath)
	if err == nil {
		t.Errorf("Expected error when joining non-existent file, got nil")
		os.Remove(joinedPath)
	}
}

func TestJoinerWithProgressBar(t *testing.T) {
	originalDisplayProgress := ui.DisplayProgress
	ui.DisplayProgress = true
	defer func() {
		ui.DisplayProgress = originalDisplayProgress
	}()

	tempDir := t.TempDir()

	file1Path := filepath.Join(tempDir, "file1")
	file2Path := filepath.Join(tempDir, "file2")
	joinedPath := filepath.Join(tempDir, "joined")

	if err := os.WriteFile(file1Path, []byte("content1"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	if err := os.WriteFile(file2Path, []byte("content2"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	files := []string{file1Path, file2Path}
	err := JoinFile(files, joinedPath)
	if err != nil {
		t.Fatalf("JoinFile() with progress bar failed: %v", err)
	}

	joinedContent, err := os.ReadFile(joinedPath)
	if err != nil {
		t.Fatalf("Failed to read joined file: %v", err)
	}

	expectedContent := "content1content2"
	if string(joinedContent) != expectedContent {
		t.Errorf("Expected joined content '%s', got '%s'", expectedContent, string(joinedContent))
	}
}
