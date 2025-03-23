package main

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// TaskPrint reads and prints data about current download jobs.
func TaskPrint() error {
	usr, err := user.Current()
	FatalCheck(err)
	homeDir := usr.HomeDir

	downloadingPath := filepath.Join(homeDir, dataFolder)
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
	Printf("Currently on going download(s):\n")
	fmt.Println(folderString)

	return nil
}

func Resume(task string) (*State, error) {
	state, err := Read(task)
	if err != nil {
		return nil, err
	}

	for i, part := range state.Parts {
		fi, err := os.Stat(part.Path)
		if err != nil {
			continue
		}
		downloaded := fi.Size()
		newFrom := min(part.RangeFrom + downloaded, part.RangeTo)
		Printf("Resuming part %d: skipping %d bytes, new start offset: %d\n", part.Index, downloaded, newFrom)
		state.Parts[i].RangeFrom = newFrom
	}

	return state, nil
}
