package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/imkira/go-task"
	"github.com/mattn/go-isatty"
)

var displayProgress = true

func main() {
	// var err error
	var proxy, filePath, bwLimit, resumeTask string

	conn := flag.Int("n", runtime.NumCPU(), "number of connections")
	skiptls := flag.Bool("skip-tls", false, "skip certificate verification for https")
	flag.StringVar(&proxy, "proxy", "", "proxy for downloading, e.g. -proxy '127.0.0.1:12345' for socks5 or -proxy 'http://proxy.com:8080' for http proxy")
	flag.StringVar(&filePath, "file", "", "path to a file that contains one URL per line")
	flag.StringVar(&bwLimit, "rate", "", "bandwidth limit during download, e.g. -rate 10kB or -rate 10MiB")
	flag.StringVar(&resumeTask, "resume", "", "resume download task with given task name (or URL)")
	probe := flag.String("probe", "", "probe URL for range and content-length without downloading")
	timeout := flag.Duration("timeout", 15*time.Second, "timeout for awaiting response headers (e.g., 30s, 1m)")

	flag.Parse()
	args := flag.Args()

	// Probe diagnostics mode
	if *probe != "" {
		DebugProbe(*probe, *skiptls, proxy, *timeout)
		return
	}

	// If the resume flag is provided, use that path (ignoring other arguments)
	if resumeTask != "" {
		state, err := Resume(resumeTask)
		if err != nil {
			if !os.IsNotExist(err) {
				Errorf("Resume failed: %v\n", err)
				os.Exit(1)
			}
			// No state.json — try to reconstruct from existing part files.
			state, err = ReconstructStateFromParts(resumeTask, *skiptls, proxy, *timeout)
			if err == nil {
				Printf("Reconstructed state from %d part files — resuming.\n", len(state.Parts))
				runWithTUI(func() {
					Execute(state.URL, state, *conn, *skiptls, proxy, bwLimit, *timeout)
				}, *conn)
				return
			}
			// No part files either — start fresh if it looks like a URL.
			Warnf("No saved state found for %q — starting fresh download.\n", resumeTask)
			if !IsURL(resumeTask) {
				Errorf("No saved state found for task %q and it is not a URL.\n", resumeTask)
				os.Exit(1)
			}
			runWithTUI(func() {
				Execute(resumeTask, nil, *conn, *skiptls, proxy, bwLimit, *timeout)
			}, *conn)
			return
		}
		runWithTUI(func() {
			Execute(state.URL, state, *conn, *skiptls, proxy, bwLimit, *timeout)
		}, *conn)
		return
	}

	// If no resume flag, then check for positional URL or file input
	if len(args) < 1 {
		if len(filePath) < 1 {
			Errorln("A URL or input file with URLs is required")
			usage()
			os.Exit(1)
		}
		// Create a serial group for processing multiple URLs in a file.
		g1 := task.NewSerialGroup()
		file, err := os.Open(filePath)
		if err != nil {
			FatalCheck(err)
		}
		defer file.Close()

		reader := bufio.NewReader(file)
		for {
			line, _, err := reader.ReadLine()
			if err == io.EOF {
				break
			}
			url := strings.TrimSpace(string(line))
			if url == "" || strings.HasPrefix(url, "#") {
				continue
			}
			// Add the download task for each URL
			g1.AddChild(downloadTask(url, nil, *conn, *skiptls, proxy, bwLimit, *timeout))
		}
		g1.Run(nil)
		return
	}

	// Otherwise, if a URL is provided as positional argument, treat it as a new download.
	downloadURL := args[0]
	// Check if a folder already exists for the task and remove if necessary.
	if ExistDir(FolderOf(downloadURL)) {
		Warnf("Downloading task already exists, remove it first\n")
		err := os.RemoveAll(FolderOf(downloadURL))
		FatalCheck(err)
	}
	runWithTUI(func() {
		Execute(downloadURL, nil, *conn, *skiptls, proxy, bwLimit, *timeout)
	}, *conn)
}

// runWithTUI starts a Bubble Tea program for interactive TTY sessions and runs fn
// in a background goroutine. Falls back to plain execution when not in a TTY.
func runWithTUI(fn func(), numConns int) {
	if isatty.IsTerminal(os.Stdout.Fd()) && displayProgress {
		model := newTUIModel(numConns)
		p := tea.NewProgram(model, tea.WithAltScreen())
		Program = p
		go func() {
			var downloadErr error
			defer func() {
				if r := recover(); r != nil {
					if err, ok := r.(error); ok {
						downloadErr = err
					} else {
						downloadErr = fmt.Errorf("%v", r)
					}
				}
				if downloadErr != nil {
					p.Send(DownloadErrorMsg{Err: downloadErr})
				} else {
					p.Send(DownloadDoneMsg{})
				}
			}()
			fn()
		}()
		if _, err := p.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "TUI error:", err)
			os.Exit(1)
		}
		return
	}
	// Non-TTY: run directly.
	fn()
}

func downloadTask(url string, state *State, conn int, skiptls bool, proxy string, bwLimit string, timeout time.Duration) task.Task {
	run := func(t task.Task, ctx task.Context) {
		Execute(url, state, conn, skiptls, proxy, bwLimit, timeout)
	}
	return task.NewTaskWithFunc(run)
}

// Execute configures the HTTPDownloader and uses it to download the target.
func Execute(url string, state *State, conn int, skiptls bool, proxy string, bwLimit string, timeout time.Duration) {
	// Capture OS interrupt signals
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)
	defer signal.Stop(signalChan)

	var isInterrupted = false

	var downloader *HTTPDownloader
	if state == nil {
		downloader = NewHTTPDownloader(url, conn, skiptls, proxy, bwLimit, timeout)
	} else {
		// Rebuild client with current connection settings so proxy/TLS/timeout are honoured.
		client := ProxyAwareHTTPClient(proxy, skiptls, timeout)
		downloader = &HTTPDownloader{
			url:       state.URL,
			file:      TaskFromURL(state.URL),
			par:       int64(len(state.Parts)),
			len:       0, // unknown at resume time; open-ended range used for last part
			parts:     state.Parts,
			resumable: true,
			proxy:     proxy,
			skipTLS:   skiptls,
			timeout:   timeout,
			client:    client,
		}
		if Program != nil {
			Program.Send(DownloadStartMsg{
				URL:      state.URL,
				FileName: TaskFromURL(state.URL),
				NumParts: len(state.Parts),
			})
		}
	}

	numParts := len(downloader.parts)

	doneChan := make(chan bool, 1)
	fileChan := make(chan string, numParts)
	errorChan := make(chan error, numParts)
	stateChan := make(chan Part, numParts)
	interruptChan := make(chan bool, numParts)

	go downloader.Do(doneChan, fileChan, errorChan, interruptChan, stateChan)

	var parts []Part
	var files []string

loop:
	for {
		select {
		case <-signalChan:
			if !isInterrupted {
				isInterrupted = true
				for i := 0; i < numParts; i++ {
					interruptChan <- true
				}
			}
		case file := <-fileChan:
			files = append(files, file)
		case err := <-errorChan:
			Errorf("%v\n", err)
			if Program != nil {
				Program.Send(DownloadErrorMsg{Err: err})
			} else {
				os.Exit(1)
			}
			return
		case part := <-stateChan:
			parts = append(parts, part)
		case <-doneChan:
			// Drain remaining in-flight notifications.
			for len(files) < numParts {
				files = append(files, <-fileChan)
			}
			for len(parts) < numParts {
				parts = append(parts, <-stateChan)
			}
			break loop
		}
	}

	if isInterrupted {
		if downloader.resumable {
			Printf("Interrupted — saving state…\n")
			s := &State{URL: url, Parts: parts}
			if err := s.Save(); err != nil {
				Errorf("Save failed: %v\n", err)
			}
		} else {
			Warnf("Interrupted, download is not resumable.\n")
		}
		return
	}

	// Collect part files from the temp folder (source of truth, avoids channel races).
	folder := FolderOf(url)
	entries, readErr := os.ReadDir(folder)
	FatalCheck(readErr)
	files = files[:0]
	prefix := TaskFromURL(url) + ".part"
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			files = append(files, folder+string(os.PathSeparator)+e.Name())
		}
	}

	if err := JoinFile(files, TaskFromURL(url)); err != nil {
		Errorf("Join failed: %v\n", err)
		if Program != nil {
			Program.Send(DownloadErrorMsg{Err: err})
		}
		return
	}
	if err := os.RemoveAll(FolderOf(url)); err != nil {
		Warnf("Cleanup failed: %v\n", err)
	}
}

func usage() {
	Printf(`Usage:
 hget [options] URL
 hget [options] --resume=TaskName

 Options:
   -n int          number of connections (default number of CPUs)
   -skip-tls bool  skip certificate verification for https (default false)
   -proxy string   proxy address (e.g., '127.0.0.1:12345' for socks5 or 'http://proxy.com:8080')
   -file string    file path containing URLs (one per line)
   -rate string    bandwidth limit during download (e.g., 10kB, 10MiB)
   -resume string  resume a stopped download by providing its task name or URL
   -probe string   probe URL for range and content-length without downloading
   -timeout duration timeout for awaiting response headers (default 15s)
 `)
}
