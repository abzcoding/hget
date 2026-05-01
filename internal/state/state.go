package state

import (
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"

	"github.com/abzcoding/hget/internal/util"
)

// DataFolder is the subdirectory under $HOME used to store download state.
var DataFolder = ".hget/"

// StateFileName is the name of the persisted state file inside DataFolder.
var StateFileName = "state.json"

// State holds information about url Parts.
type State struct {
	URL   string
	Parts []Part
}

// Part represents a chunk of downloaded file.
type Part struct {
	Index     int64
	URL       string
	Path      string
	RangeFrom int64
	RangeTo   int64
}

// FolderOf returns the state-data directory for the given URL, guarding
// against directory traversal.
func FolderOf(urlStr string) string {
	usr, err := user.Current()
	util.FatalCheck(err)
	return util.SafeFolderPath(usr.HomeDir, DataFolder, urlStr)
}

// Save persists the download state and moves in-progress part files into the
// state directory.
func (s *State) Save() error {
	folder := FolderOf(s.URL)
	if err := util.MkdirIfNotExist(folder); err != nil {
		return err
	}

	for _, part := range s.Parts {
		err := os.Rename(part.Path, filepath.Join(folder, filepath.Base(part.Path)))
		if err != nil {
			return err
		}
	}

	j, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(folder, StateFileName), j, 0644)
}

// Read loads a previously saved State for the given task name or URL.
func Read(task string) (*State, error) {
	usr, err := user.Current()
	util.FatalCheck(err)
	homeDir := usr.HomeDir

	taskName := util.TaskFromURL(task)
	file := filepath.Join(homeDir, DataFolder, taskName, StateFileName)

	bytes, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	s := new(State)
	err = json.Unmarshal(bytes, s)
	return s, err
}
