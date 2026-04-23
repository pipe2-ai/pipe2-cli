package main

import (
	"fmt"
	"os"

	"github.com/pipe2-ai/pipe2-cli/internal/cli"
)

// version is injected at build time via -ldflags.
var version = "dev"

func main() {
	if err := cli.NewRootCmd(version).Execute(); err != nil {
		// Cobra already prints the error; just exit non-zero.
		// Use code 2 for usage errors, 1 for execution errors. cli.Execute
		// returns the most-specific exit code via cli.ExitError.
		fmt.Fprintln(os.Stderr)
		os.Exit(cli.ExitCodeFor(err))
	}
}
