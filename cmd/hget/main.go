package main

import (
	"flag"
	"os"
	"runtime"
	"time"

	"github.com/abzcoding/hget/internal/batch"
	"github.com/abzcoding/hget/internal/downloader"
	"github.com/abzcoding/hget/internal/state"
	"github.com/abzcoding/hget/internal/ui"
	"github.com/abzcoding/hget/internal/util"
)

// GitCommit is injected at build time via -ldflags.
var GitCommit string

func main() {
	flag.Usage = ui.PrintHelp

	var proxy, filePath, bwLimit, resumeTask string

	conn := flag.Int("n", runtime.NumCPU(), "number of connections")
	skiptls := flag.Bool("skip-tls", false, "skip certificate verification for https")
	verify := flag.Bool("verify", false, "download and verify the .sig GPG signature after download")
	flag.StringVar(&proxy, "proxy", "", "proxy for downloading, e.g. -proxy '127.0.0.1:12345' for socks5 or -proxy 'http://proxy.com:8080' for http proxy")
	flag.StringVar(&filePath, "file", "", "path to a file that contains one URL per line")
	flag.StringVar(&bwLimit, "rate", "", "bandwidth limit during download, e.g. -rate 10kB or -rate 10MiB")
	flag.StringVar(&resumeTask, "resume", "", "resume download task with given task name (or URL)")
	probe := flag.String("probe", "", "probe URL for range and content-length without downloading")
	timeout := flag.Duration("timeout", 15*time.Second, "timeout for awaiting response headers (e.g., 30s, 1m)")

	flag.Parse()
	args := flag.Args()

	// Probe diagnostics mode.
	if *probe != "" {
		downloader.DebugProbe(*probe, *skiptls, proxy, *timeout)
		return
	}

	// Resume mode.
	if resumeTask != "" {
		st, err := state.Resume(resumeTask)
		if err != nil {
			if !os.IsNotExist(err) {
				ui.Errorf("Resume failed: %v\n", err)
				os.Exit(1)
			}
			// No state.json — try to reconstruct from existing part files.
			st, err = downloader.ReconstructStateFromParts(resumeTask, *skiptls, proxy, *timeout)
			if err == nil {
				ui.Printf("Reconstructed state from %d part files — resuming.\n", len(st.Parts))
				ui.RunWithTUI(func() {
					downloader.Execute(st.URL, st, *conn, *skiptls, proxy, bwLimit, *timeout)
				}, *conn, false, 0, 0)
				return
			}
			// No part files either — start fresh if it looks like a URL.
			ui.Warnf("No saved state found for %q — starting fresh download.\n", resumeTask)
			if !util.IsURL(resumeTask) {
				ui.Errorf("No saved state found for task %q and it is not a URL.\n", resumeTask)
				os.Exit(1)
			}
			ui.RunWithTUI(func() {
				downloader.Execute(resumeTask, nil, *conn, *skiptls, proxy, bwLimit, *timeout)
			}, *conn, false, 0, 0)
			return
		}
		ui.RunWithTUI(func() {
			downloader.Execute(st.URL, st, *conn, *skiptls, proxy, bwLimit, *timeout)
		}, *conn, false, 0, 0)
		return
	}

	// Batch mode (--file).
	if len(args) < 1 {
		if len(filePath) < 1 {
			ui.Errorln("A URL or input file with URLs is required")
			ui.PrintHelp()
			os.Exit(1)
		}
		batch.RunBatchDownloads(filePath, *conn, *skiptls, proxy, bwLimit, *timeout, *verify)
		return
	}

	// Single URL download.
	downloadURL := args[0]
	destFile := util.TaskFromURL(downloadURL)

	if _, err := os.Stat(destFile); err == nil {
		if !ui.ConfirmRedownload(destFile) {
			ui.Warnf("Skipping download — %s already exists.\n", destFile)
			if *verify {
				ok, detail := downloader.RunVerify(downloadURL, *skiptls, proxy, *timeout)
				ui.PrintVerifySummary(ok, detail)
			}
			return
		}
	}

	if util.ExistDir(state.FolderOf(downloadURL)) {
		ui.Warnf("Downloading task already exists, remove it first\n")
		err := os.RemoveAll(state.FolderOf(downloadURL))
		util.FatalCheck(err)
	}

	var verifyOK bool
	var verifyDetail string
	var didVerify bool

	ui.RunWithTUI(func() {
		downloader.Execute(downloadURL, nil, *conn, *skiptls, proxy, bwLimit, *timeout)
		if *verify {
			verifyOK, verifyDetail = downloader.RunVerify(downloadURL, *skiptls, proxy, *timeout)
			didVerify = true
		}
	}, *conn, *verify, 0, 0)

	if didVerify {
		ui.PrintVerifySummary(verifyOK, verifyDetail)
	}
}
