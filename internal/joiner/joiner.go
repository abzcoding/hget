package joiner

import (
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/abzcoding/hget/internal/ui"
)

func JoinFile(files []string, out string) (err error) {
	sort.Strings(files)

	ui.Printf("Start joining %d parts\n", len(files))
	if ui.Program != nil {
		ui.Program.Send(ui.JoinStartMsg{Total: len(files)})
	}

	outf, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	if err != nil {
		return err
	}
	// Ignoring Close on a writable file can mask flush errors that lead to
	// silent data loss on a full disk; surface them to the caller.
	defer func() {
		if cerr := outf.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("closing output file: %w", cerr)
		}
	}()

	for i, f := range files {
		if err = copyChunk(f, outf); err != nil {
			return err
		}
		if ui.Program != nil {
			ui.Program.Send(ui.JoinProgressMsg{Current: i + 1})
		}
	}

	if err = outf.Sync(); err != nil {
		return fmt.Errorf("flushing output file: %w", err)
	}

	if ui.Program != nil {
		ui.Program.Send(ui.JoinDoneMsg{})
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
