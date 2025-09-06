package main

import (
	"io"
	"os"
	"sort"

	"github.com/fatih/color"
	"gopkg.in/cheggaaa/pb.v1"
)

// JoinFile joins seperate chunks of file and forms the final downloaded artifact
func JoinFile(files []string, out string) error {
	//sort with file name or we will join files with wrong order
	sort.Strings(files)
	var bar *pb.ProgressBar

	if DisplayProgressBar() {
		Printf("Start joining \n")
		bar = pb.StartNew(len(files)).Prefix(color.CyanString("Joining"))
	}

	outf, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer outf.Close()

	for _, f := range files {
		if err = copy(f, outf); err != nil {
			return err
		}
		if DisplayProgressBar() {
			bar.Increment()
		}
	}

	if DisplayProgressBar() {
		bar.Finish()
	}

	return nil
}

// this function split just to use defer
func copy(from string, to io.Writer) error {
	f, err := os.OpenFile(from, os.O_RDONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	io.Copy(to, f)
	return nil
}
