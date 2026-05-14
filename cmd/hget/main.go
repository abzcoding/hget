package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/abzcoding/hget/internal/batch"
	"github.com/abzcoding/hget/internal/downloader"
	"github.com/abzcoding/hget/internal/extractor"
	"github.com/abzcoding/hget/internal/state"
	"github.com/abzcoding/hget/internal/ui"
	"github.com/abzcoding/hget/internal/util"
)

// GitCommit is injected at build time via -ldflags.
var GitCommit string

func main() {
	flag.Usage = ui.PrintHelp

	var proxy, filePath, bwLimit, resumeTask, extractorMode, cookiesFile, cookiesBrowser string

	conn := flag.Int("n", runtime.NumCPU(), "number of connections")
	skiptls := flag.Bool("skip-tls", false, "skip certificate verification for https")
	verify := flag.Bool("verify", false, "download and verify the .sig GPG signature after download")
	flag.StringVar(&proxy, "proxy", "", "proxy for downloading, e.g. -proxy '127.0.0.1:12345' for socks5 or -proxy 'http://proxy.com:8080' for http proxy")
	flag.StringVar(&filePath, "file", "", "path to a file that contains one URL per line")
	flag.StringVar(&bwLimit, "rate", "", "bandwidth limit during download, e.g. -rate 10kB or -rate 10MiB")
	flag.StringVar(&resumeTask, "resume", "", "resume download task with given task name (or URL)")
	flag.StringVar(&extractorMode, "extractor", "auto", "extractor mode: auto | yt-dlp | none (auto picks yt-dlp for known media hosts)")
	flag.StringVar(&cookiesFile, "cookies", "", "path to Netscape-format cookies.txt for the extractor (forwarded to yt-dlp --cookies)")
	flag.StringVar(&cookiesBrowser, "cookies-from-browser", "", "browser to extract cookies from for the extractor, e.g. firefox, chrome:Default (forwarded to yt-dlp --cookies-from-browser)")
	probe := flag.String("probe", "", "probe URL for range and content-length without downloading")
	timeout := flag.Duration("timeout", 15*time.Second, "timeout for awaiting response headers (e.g., 30s, 1m)")

	flag.Parse()
	args := flag.Args()

	// Probe diagnostics mode.
	if *probe != "" {
		downloader.DebugProbe(*probe, *skiptls, proxy, *timeout)
		return
	}

	// Top-level cancellation context.  External SIGINT/SIGTERM/SIGHUP/SIGQUIT
	// cancel this context with cause = downloader.ErrAbortBatch, which both
	// the TUI and the downloader/batch loop observe.
	rootCtx, rootCancel := context.WithCancelCause(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		rootCancel(downloader.ErrAbortBatch)
	}()
	defer rootCancel(nil)

	// Resume mode.
	if resumeTask != "" {
		runResume(rootCtx, resumeTask, *conn, *skiptls, proxy, bwLimit, *timeout)
		return
	}

	// Batch mode (--file).
	if len(args) < 1 {
		if len(filePath) < 1 {
			ui.Errorln("A URL or input file with URLs is required")
			ui.PrintHelp()
			os.Exit(1)
		}
		batch.RunBatchDownloads(rootCtx, filePath, *conn, *skiptls, proxy, bwLimit, *timeout, *verify)
		return
	}

	// Single URL download.
	downloadURL := args[0]

	// Extractor mode (yt-dlp pipeline).  Picked when explicitly forced
	// or when --extractor=auto and the URL host matches a known media
	// site that hget's HTTP engine can't handle directly (YouTube etc.).
	if shouldUseExtractor(extractorMode, downloadURL) {
		runExtractor(rootCtx, downloadURL, extractor.Options{
			CookiesFile:        cookiesFile,
			CookiesFromBrowser: cookiesBrowser,
		})
		return
	}

	destFile := util.TaskFromURL(downloadURL)

	// Check if final file already exists
	if _, err := os.Stat(destFile); err == nil {
		if !ui.ConfirmRedownload(destFile) {
			ui.ShowMessage(ui.MessageInfo, "DOWNLOAD SKIPPED", fmt.Sprintf("File already exists: %s", destFile))
			if *verify {
				ok, detail := downloader.RunVerify(rootCtx, downloadURL, *skiptls, proxy, *timeout)
				ui.PrintVerifySummary(ok, detail)
			}
			return
		}
		// User wants to redownload — clean up any partial state
		if util.ExistDir(state.FolderOf(downloadURL)) {
			err := os.RemoveAll(state.FolderOf(downloadURL))
			util.FatalCheck(err)
		}
	}

	// Check for resumable partial download
	var st *state.State
	if state.Exists(downloadURL) {
		st, _ = state.PromptResume(downloadURL)
	}

	itemCtx, cancelItem := context.WithCancelCause(rootCtx)
	defer cancelItem(nil)

	var verifyOK bool
	var verifyDetail string
	var didVerify bool

	runErr := ui.RunWithTUI(ui.RunOptions{
		Ctx:          itemCtx,
		OnQuit:       func() { cancelItem(downloader.ErrUserQuit) },
		NumConns:     *conn,
		WillVerify:   *verify,
		BatchCurrent: 0,
		BatchTotal:   0,
	}, func() error {
		if err := downloader.Execute(itemCtx, downloadURL, st, *conn, *skiptls, proxy, bwLimit, *timeout); err != nil {
			return err
		}
		if *verify {
			verifyOK, verifyDetail = downloader.RunVerify(itemCtx, downloadURL, *skiptls, proxy, *timeout)
			didVerify = true
		}
		return nil
	})

	if didVerify {
		ui.PrintVerifySummary(verifyOK, verifyDetail)
	}

	if runErr != nil &&
		!errors.Is(runErr, downloader.ErrUserQuit) &&
		!errors.Is(runErr, downloader.ErrAbortBatch) &&
		!errors.Is(runErr, context.Canceled) {
		os.Exit(1)
	}
}

// runResume handles --resume in both forms (task-name and URL).
func runResume(rootCtx context.Context, resumeTask string, conn int, skiptls bool, proxy, bwLimit string, timeout time.Duration) {
	st, err := state.Resume(resumeTask)
	if err != nil {
		if !os.IsNotExist(err) {
			ui.ShowMessage(ui.MessageError, "RESUME FAILED", fmt.Sprintf("Could not load saved state: %v", err))
			os.Exit(1)
		}
		// No state.json — try to reconstruct from existing part files.
		st, err = downloader.ReconstructStateFromParts(rootCtx, resumeTask, skiptls, proxy, timeout)
		if err == nil {
			ui.ShowMessage(ui.MessageInfo, "STATE RECONSTRUCTED", fmt.Sprintf("Recovered %d part files — resuming download", len(st.Parts)))
			runOne(rootCtx, st.URL, st, conn, skiptls, proxy, bwLimit, timeout)
			return
		}
		// No part files either — start fresh if it looks like a URL.
		ui.ShowMessage(ui.MessageWarning, "NO SAVED STATE", fmt.Sprintf("Starting fresh download for: %s", resumeTask))
		if !util.IsURL(resumeTask) {
			ui.ShowMessage(ui.MessageError, "INVALID TASK", fmt.Sprintf("No saved state found and not a valid URL: %s", resumeTask))
			os.Exit(1)
		}
		runOne(rootCtx, resumeTask, nil, conn, skiptls, proxy, bwLimit, timeout)
		return
	}
	runOne(rootCtx, st.URL, st, conn, skiptls, proxy, bwLimit, timeout)
}

// shouldUseExtractor decides whether the URL should be routed through
// the yt-dlp pipeline.  Modes:
//   - "yt-dlp": always use yt-dlp
//   - "auto":   use yt-dlp when the host looks like a media site
//   - "none":   never use yt-dlp (force the plain HTTP engine)
func shouldUseExtractor(mode, url string) bool {
	switch mode {
	case "yt-dlp", "ytdlp":
		return true
	case "none", "off", "false":
		return false
	default: // "auto" and unknown values fall through to detection
		return extractor.LooksExtractable(url)
	}
}

// runExtractor drives the yt-dlp pipeline behind the VCR + Mixer TUI.
// On success the resolved output file is left in the current working
// directory (yt-dlp's default), matching hget's existing behaviour.
//
// Cookie sources are validated BEFORE we start the TUI so a typo'd path
// produces a clean error in the terminal instead of a cryptic message
// flashing inside the alt-screen for half a second before exit.
func runExtractor(rootCtx context.Context, url string, opts extractor.Options) {
	if opts.CookiesFile != "" {
		if _, err := os.Stat(opts.CookiesFile); err != nil {
			ui.ShowMessage(ui.MessageError, "COOKIES FILE NOT FOUND",
				fmt.Sprintf("--cookies %s: %v", opts.CookiesFile, err))
			os.Exit(1)
		}
	}
	if opts.CookiesFile != "" && opts.CookiesFromBrowser != "" {
		ui.ShowMessage(ui.MessageWarning, "COOKIE SOURCES",
			"both --cookies and --cookies-from-browser are set; yt-dlp will pick the browser source")
	}

	itemCtx, cancelItem := context.WithCancelCause(rootCtx)
	defer cancelItem(nil)

	err := ui.RunExtractorTUI(ui.ExtractorRunOptions{
		Ctx:    itemCtx,
		URL:    url,
		OnQuit: func() { cancelItem(downloader.ErrUserQuit) },
	}, func(sel ui.ExtractorSelector) error {
		// The selector function bridges the TUI's "user pressed REC"
		// event into extractor.Pipeline's SelectorFunc contract.
		// Empty spec/container ("use defaults") flow through unchanged.
		picker := func(ctx context.Context, _ extractor.Meta) (extractor.FormatSelection, error) {
			spec, container, err := sel(ctx)
			if err != nil {
				return extractor.FormatSelection{}, err
			}
			return extractor.FormatSelection{Spec: spec, Container: container}, nil
		}
		return extractor.Pipeline(itemCtx, url, "", opts, picker)
	})

	if err != nil &&
		!errors.Is(err, downloader.ErrUserQuit) &&
		!errors.Is(err, downloader.ErrAbortBatch) &&
		!errors.Is(err, context.Canceled) {
		ui.Errorln(err)
		os.Exit(1)
	}
}

func runOne(rootCtx context.Context, url string, st *state.State, conn int, skiptls bool, proxy, bwLimit string, timeout time.Duration) {
	itemCtx, cancelItem := context.WithCancelCause(rootCtx)
	defer cancelItem(nil)

	_ = ui.RunWithTUI(ui.RunOptions{
		Ctx:        itemCtx,
		OnQuit:     func() { cancelItem(downloader.ErrUserQuit) },
		NumConns:   conn,
		WillVerify: false,
	}, func() error {
		return downloader.Execute(itemCtx, url, st, conn, skiptls, proxy, bwLimit, timeout)
	})
}
