package downloader

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/abzcoding/hget/internal/state"
	"github.com/abzcoding/hget/internal/ui"
	"github.com/abzcoding/hget/internal/util"
)

// ReconstructStateFromParts rebuilds a *State for a URL that has existing part
// files in the download folder but no state.json (e.g. killed with SIGKILL).
// It probes the server for the total content length, recalculates the original
// part boundaries, then advances RangeFrom by each part file's current size.
func ReconstructStateFromParts(url string, skiptls bool, proxyServer string, timeout time.Duration) (*state.State, error) {
	folder := state.FolderOf(url)
	entries, err := os.ReadDir(folder)
	if err != nil {
		return nil, err
	}

	file := util.TaskFromURL(url)
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
	client := ProxyAwareHTTPClient(proxyServer, skiptls, timeout)
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
	parts := PartCalculate(numParts, totalLen, url)

	// Advance RangeFrom by the bytes already on disk for each part.
	for i := range parts {
		fi, serr := os.Stat(partFiles[i])
		if serr != nil {
			ui.Printf("Part %d file missing (%s); will start from original offset %d\n",
				i, partFiles[i], parts[i].RangeFrom)
			continue
		}
		sz := fi.Size()
		if sz > 0 {
			parts[i].RangeFrom += sz
			ui.Printf("Reconstructed part %d: resuming from byte %d (file has %d bytes)\n",
				i, parts[i].RangeFrom, sz)
		}
	}

	return &state.State{URL: url, Parts: parts}, nil
}
