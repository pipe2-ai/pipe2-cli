package cli

import (
	"os"
)

// isTerminal returns true if f is connected to a terminal. We avoid pulling
// in golang.org/x/term to keep the dep tree minimal — checking the file mode
// is enough for the "agent piping stdout" detection we care about.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
