package main

import (
	"bufio"
	"flag"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/imkira/go-task"
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

	flag.Parse()
	args := flag.Args()

	// If the resume flag is provided, use that path (ignoring other arguments)
	if resumeTask != "" {
		state, err := Resume(resumeTask)
		FatalCheck(err)
		Execute(state.URL, state, *conn, *skiptls, proxy, bwLimit)
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
			url := string(line)
			// Add the download task for each URL
			g1.AddChild(downloadTask(url, nil, *conn, *skiptls, proxy, bwLimit))
		}
		g1.Run(nil)
		return
	}

	// Otherwise, if a URL is provided as positional argument, treat it as a new download.
	downloadURL := args[0]
	// Check if a folder already exists for the task and remove if necessary.
	if ExistDir(FolderOf(downloadURL)) {
		Warnf("Downloading task already exists, remove it first \n")
		err := os.RemoveAll(FolderOf(downloadURL))
		FatalCheck(err)
	}
	Execute(downloadURL, nil, *conn, *skiptls, proxy, bwLimit)
}

func downloadTask(url string, state *State, conn int, skiptls bool, proxy string, bwLimit string) task.Task {
	run := func(t task.Task, ctx task.Context) {
		Execute(url, state, conn, skiptls, proxy, bwLimit)
	}
	return task.NewTaskWithFunc(run)
}

// Execute configures the HTTPDownloader and uses it to download the target.
func Execute(url string, state *State, conn int, skiptls bool, proxy string, bwLimit string) {
	// Capture OS interrupt signals
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)

	var files = make([]string, 0)
	var parts = make([]Part, 0)
	var isInterrupted = false

	doneChan := make(chan bool, conn)
	fileChan := make(chan string, conn)
	errorChan := make(chan error, 1)
	stateChan := make(chan Part, 1)
	interruptChan := make(chan bool, conn)

	var downloader *HTTPDownloader
	if state == nil {
		downloader = NewHTTPDownloader(url, conn, skiptls, proxy, bwLimit)
	} else {
		downloader = &HTTPDownloader{
			url:       state.URL,
			file:      TaskFromURL(state.URL),
			par:       int64(len(state.Parts)),
			parts:     state.Parts,
			resumable: true,
		}
	}
	go downloader.Do(doneChan, fileChan, errorChan, interruptChan, stateChan)

	for {
		select {
		case <-signalChan:
			// Signal all active download routines to interrupt.
			isInterrupted = true
			for i := 0; i < conn; i++ {
				interruptChan <- true
			}
		case file := <-fileChan:
			files = append(files, file)
		case err := <-errorChan:
			Errorf("%v", err)
			panic(err)
		case part := <-stateChan:
			parts = append(parts, part)
		case <-doneChan:
			if isInterrupted {
				if downloader.resumable {
					Printf("Interrupted, saving state...\n")
					s := &State{URL: url, Parts: parts}
					if err := s.Save(); err != nil {
						Errorf("%v\n", err)
					}
				} else {
					Warnf("Interrupted, but the download is not resumable. Exiting silently.\n")
				}
			} else {
				err := JoinFile(files, TaskFromURL(url))
				FatalCheck(err)
				err = os.RemoveAll(FolderOf(url))
				FatalCheck(err)
			}
			return
		}
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
`)
}
