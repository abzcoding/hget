package main

import (
	"fmt"
	"io"
	"os"
	"sort"
)

// JoinFile joins separate chunks and assembles the final downloaded artifact.
func JoinFile(files []string, out string) error {
	sort.Strings(files)

	Printf("Start joining %d parts\n", len(files))
	if Program != nil {
		Program.Send(JoinStartMsg{Total: len(files)})
	}

	outf, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer outf.Close()

	for i, f := range files {
		if err = copyChunk(f, outf); err != nil {
			return err
		}
		if Program != nil {
			Program.Send(JoinProgressMsg{Current: i + 1})
		}
	}

	if Program != nil {
		Program.Send(JoinDoneMsg{})
	}
	return nil
}

// copyChunk copies the contents of a single part file into the destination writer.
func copyChunk(from string, to io.Writer) error {
	f, err := os.OpenFile(from, os.O_RDONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(to, f); err != nil {
		return fmt.Errorf("copying %s: %w", from, err)
	}
	return nil
}
