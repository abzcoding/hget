package main

import (
	"fmt"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
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
		// state.Parts[i].RangeFrom was saved as (original_from + bytes_downloaded)
		// when the download was interrupted — it already equals the next byte to request.
		fi, err := os.Stat(part.Path)
		if err != nil {
			Warnf("Part %d file not found (%s), it will be re-downloaded from offset %d\n",
				part.Index, part.Path, part.RangeFrom)
			continue
		}
		Printf("Resuming part %d from byte %d (file has %d bytes)\n",
			part.Index, part.RangeFrom, fi.Size())
		_ = state.Parts[i]
	}

	return state, nil
}

// ReconstructStateFromParts rebuilds a *State for a URL that has existing part
// files in the download folder but no state.json (e.g. killed with SIGKILL).
// It probes the server for the total content length, recalculates the original
// part boundaries, then advances RangeFrom by each part file's current size.
func ReconstructStateFromParts(url string, skiptls bool, proxy string, timeout time.Duration) (*State, error) {
	folder := FolderOf(url)
	entries, err := os.ReadDir(folder)
	if err != nil {
		return nil, err
	}

	file := TaskFromURL(url)
	prefix := file + ".part"
	var partFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			partFiles = append(partFiles, filepath.Join(folder, e.Name()))
		}
	}
	if len(partFiles) == 0 {
		return nil, fmt.Errorf("no part files found in %s", folder)
	}
	sort.Strings(partFiles)
	numParts := int64(len(partFiles))

	// Probe server for content-length.
	client := ProxyAwareHTTPClient(proxy, skiptls, timeout)
	var totalLen int64
	req, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		return nil, fmt.Errorf("probe request: %w", err)
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", defaultUserAgent)
	if resp, herr := client.Do(req); herr == nil {
		defer resp.Body.Close()
		if cl := resp.Header.Get("Content-Length"); cl != "" {
			if v, perr := strconv.ParseInt(cl, 10, 64); perr == nil {
				totalLen = v
			}
		}
	}
	// Fallback: range GET probe.
	if totalLen == 0 {
		req2, _ := http.NewRequest("GET", url, nil)
		req2.Header.Set("Range", "bytes=0-0")
		req2.Header.Set("Accept-Encoding", "identity")
		req2.Header.Set("User-Agent", defaultUserAgent)
		if resp2, herr := client.Do(req2); herr == nil {
			defer resp2.Body.Close()
			cr := resp2.Header.Get("Content-Range")
			if slash := strings.LastIndex(cr, "/"); slash != -1 {
				if v, perr := strconv.ParseInt(cr[slash+1:], 10, 64); perr == nil {
					totalLen = v
				}
			}
		}
	}
	if totalLen == 0 {
		return nil, fmt.Errorf("could not determine content-length from server; cannot reconstruct state")
	}

	// Recalculate original part boundaries.
	parts := partCalculate(numParts, totalLen, url)

	// Advance RangeFrom by the bytes already on disk for each part.
	for i := range parts {
		fi, serr := os.Stat(partFiles[i])
		if serr != nil {
			Printf("Part %d file missing (%s); will start from original offset %d\n",
				i, partFiles[i], parts[i].RangeFrom)
			continue
		}
		sz := fi.Size()
		if sz > 0 {
			parts[i].RangeFrom += sz
			Printf("Reconstructed part %d: resuming from byte %d (file has %d bytes)\n",
				i, parts[i].RangeFrom, sz)
		}
	}

	return &State{URL: url, Parts: parts}, nil
}
