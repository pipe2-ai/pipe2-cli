package cli

import (
	"errors"
	"os"

	"github.com/spf13/cobra"
)

// Global flags shared across every command.
type GlobalFlags struct {
	JSON       bool
	ConfigPath string
	APIURL     string
	Token      string
	Verbose    bool
}

var Globals = &GlobalFlags{}

// NewRootCmd builds the cobra root and registers all subcommands.
// Kept exported so tests and `pipe2 schema` can introspect the tree.
func NewRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "pipe2",
		Short: "Pipe2.ai command line and agent SDK",
		Long: `pipe2 is the official Pipe2.ai command line tool.

It is designed to be invoked by both humans and AI agents:
  - Every command supports --json for structured output on stdout
  - Human-readable progress is written to stderr
  - Exit codes signal failure class (see ` + "`pipe2 help exit-codes`" + `)
  - ` + "`pipe2 schema [command]`" + ` returns a machine-readable schema
    of every command, flag, and output type — agents use this as the
    canonical source of truth for the CLI surface.`,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	root.PersistentFlags().BoolVar(&Globals.JSON, "json", false,
		"emit JSON to stdout instead of human-readable text")
	root.PersistentFlags().StringVar(&Globals.ConfigPath, "config", "",
		"path to config file (default: $XDG_CONFIG_HOME/pipe2/config.toml)")
	root.PersistentFlags().StringVar(&Globals.APIURL, "api-url", "",
		"override API base URL (env: PIPE2_API_URL)")
	root.PersistentFlags().StringVar(&Globals.Token, "token", "",
		"override personal access token (env: PIPE2_TOKEN)")
	root.PersistentFlags().BoolVarP(&Globals.Verbose, "verbose", "v", false,
		"verbose human output on stderr")

	// Default JSON to true when stdout is not a TTY — agents almost always
	// pipe stdout, so this gives them structured output without flags.
	if !isTerminal(os.Stdout) {
		Globals.JSON = true
	}

	root.AddCommand(
		newAuthCmd(),
		newPipelinesCmd(),
		newRunsCmd(),
		newAssetsCmd(),
		newCreditsCmd(),
		newSchemaCmd(),
		newSkillCmd(),
	)

	return root
}

// ExitError is a typed error that carries an explicit exit code so commands
// can signal failure class without conflating user errors with system errors.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e.Err == nil {
		return ""
	}
	return e.Err.Error()
}
func (e *ExitError) Unwrap() error { return e.Err }

// Exit code map (kept narrow on purpose):
//
//	0  success
//	1  generic execution failure (network, server error)
//	2  usage error (cobra default — bad flag, missing arg)
//	3  authentication required / invalid token
//	4  not found (404 from API)
//	5  permission denied
const (
	ExitOK            = 0
	ExitGeneric       = 1
	ExitUsage         = 2
	ExitUnauthorized  = 3
	ExitNotFound      = 4
	ExitForbidden     = 5
)

// ExitCodeFor inspects an error chain for an ExitError and returns its code,
// or ExitGeneric if none is present.
func ExitCodeFor(err error) int {
	var e *ExitError
	if errors.As(err, &e) {
		return e.Code
	}
	return ExitGeneric
}
