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
