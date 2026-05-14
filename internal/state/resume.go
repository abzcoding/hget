package state

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/abzcoding/hget/internal/ui"
	"github.com/abzcoding/hget/internal/util"
)

// Exists checks if saved state exists for the given URL or task name.
func Exists(urlOrTask string) bool {
	_, err := Read(urlOrTask)
	return err == nil
}

// PromptResume checks if resumable state exists and asks the user whether to
// resume or start fresh. Returns the state if resuming, nil if starting fresh.
func PromptResume(urlOrTask string) (*State, bool) {
	if !Exists(urlOrTask) {
		return nil, true // no state, proceed with fresh download
	}

	// Load state to get download progress
	st, err := Read(urlOrTask)
	if err != nil {
		return nil, true
	}

	// Calculate total downloaded bytes
	var downloaded int64
	var total int64
	for _, part := range st.Parts {
		downloaded += part.RangeFrom
		total += (part.RangeTo - part.RangeFrom + 1)
	}

	// Show animated TUI prompt
	resume, err := ui.ResumePrompt(util.TaskFromURL(urlOrTask), downloaded, total)
	if err != nil {
		// On error, default to fresh download
		return nil, true
	}

	if resume {
		// Validate part files
		st, err := Resume(urlOrTask)
		if err != nil {
			// If resume fails, start fresh
			folder := FolderOf(urlOrTask)
			_ = os.RemoveAll(folder)
			return nil, true
		}
		return st, true
	}

	// User chose not to resume — clean up old state
	folder := FolderOf(urlOrTask)
	if err := os.RemoveAll(folder); err != nil {
		// Silently ignore cleanup errors
	}
	return nil, true
}

// TaskPrint reads and prints data about current download jobs.
func TaskPrint() error {
	usr, err := user.Current()
	util.FatalCheck(err)
	homeDir := usr.HomeDir

	downloadingPath := filepath.Join(homeDir, DataFolder)
	downloading, err := os.ReadDir(downloadingPath)
	if err != nil {
		return err
	}

	folders := make([]string, 0)
	for _, d := range downloading {
		if d.IsDir() {
			folders = append(folders, d.Name())
		}
	}

	folderString := strings.Join(folders, "\n")
	ui.Printf("Currently on going download(s):\n")
	fmt.Println(folderString)

	return nil
}

// Resume loads the saved state for a task and validates the part files.
func Resume(task string) (*State, error) {
	state, err := Read(task)
	if err != nil {
		return nil, err
	}

	for i, part := range state.Parts {
		fi, err := os.Stat(part.Path)
		if err != nil {
			ui.Warnf("Part %d file not found (%s), it will be re-downloaded from offset %d\n",
				part.Index, part.Path, part.RangeFrom)
			continue
		}
		ui.Printf("Resuming part %d from byte %d (file has %d bytes)\n",
			part.Index, part.RangeFrom, fi.Size())
		_ = state.Parts[i]
	}

	return state, nil
}
