package main

import (
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
)

var dataFolder = ".hget/"
var stateFileName = "state.json"

// State holds information about url Parts
type State struct {
	URL   string
	Parts []Part
}

// Part represents a chunk of downloaded file
type Part struct {
	Index     int64
	URL       string
	Path      string
	RangeFrom int64
	RangeTo   int64
}

// Save stores downloaded file into disk
func (s *State) Save() error {
	//make temp folder
	//only working in unix with env HOME
	folder := FolderOf(s.URL)
	Printf("Saving current download data in %s\n", folder)
	if err := MkdirIfNotExist(folder); err != nil {
		return err
	}

	//move current downloading file to data folder
	for _, part := range s.Parts {
		err := os.Rename(part.Path, filepath.Join(folder, filepath.Base(part.Path)))
		if err != nil {
			return err
		}
	}

	//save state file
	j, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(folder, stateFileName), j, 0644)
}

// Read loads data about the state of downloaded files
func Read(task string) (*State, error) {
	usr, err := user.Current()
	FatalCheck(err)
	homeDir := usr.HomeDir

	// extract filename from task
	taskName := TaskFromURL(task)

	file := filepath.Join(homeDir, dataFolder, taskName, stateFileName)
	Printf("Getting data from %s\n", file)
	bytes, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	s := new(State)
	err = json.Unmarshal(bytes, s)
	return s, err
}
