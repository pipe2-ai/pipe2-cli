package cli

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// schemaFlag is the per-flag entry agents see when they introspect.
type schemaFlag struct {
	Name        string `json:"name"`
	Shorthand   string `json:"shorthand,omitempty"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type"`
	Default     string `json:"default,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// schemaCommand is the per-command entry. Recursive — children list child
// commands. Designed to be the canonical machine-readable surface map for
// agents, mirroring the "schema introspection" pattern from agent-CLI
// guidelines (one source of truth, not pasted into prompts).
type schemaCommand struct {
	Name        string          `json:"name"`
	Path        string          `json:"path"`
	Short       string          `json:"short,omitempty"`
	Long        string          `json:"long,omitempty"`
	Use         string          `json:"use,omitempty"`
	Aliases     []string        `json:"aliases,omitempty"`
	Args        string          `json:"args,omitempty"`
	Flags       []schemaFlag    `json:"flags,omitempty"`
	Children    []schemaCommand `json:"children,omitempty"`
	Hidden      bool            `json:"hidden,omitempty"`
	Annotations map[string]any  `json:"annotations,omitempty"`
}

func newSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schema [command-path...]",
		Short: "Dump a machine-readable schema of every command, flag, and exit code",
		Long: `Print a JSON schema of the CLI command tree.

Agents should call this once at the start of a session and treat the output
as the canonical source of truth for what commands exist, what flags they
take, and what exit codes mean. Calling with a command path narrows the
output to that subtree:

  pipe2 schema                 # full tree
  pipe2 schema pipelines       # just the pipelines subtree
  pipe2 schema pipelines run   # one command`,
		RunE: func(cmd *cobra.Command, args []string) error {
			root := cmd.Root()
			target := root
			if len(args) > 0 {
				found, _, err := root.Find(args)
				if err != nil {
					return &ExitError{Code: ExitNotFound, Err: err}
				}
				target = found
			}
			return Out(map[string]any{
				"version":    root.Version,
				"command":    walkCommand(target, ""),
				"exit_codes": exitCodeSchema(),
			})
		},
	}
}

func walkCommand(c *cobra.Command, parentPath string) schemaCommand {
	path := c.Name()
	if parentPath != "" {
		path = parentPath + " " + c.Name()
	}
	out := schemaCommand{
		Name:    c.Name(),
		Path:    path,
		Short:   c.Short,
		Long:    c.Long,
		Use:     c.Use,
		Aliases: c.Aliases,
		Hidden:  c.Hidden,
	}
	if c.Args != nil {
		// Cobra doesn't expose the validator name; we can only signal
		// "this command takes positional args".
		out.Args = "see use string"
	}
	c.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		out.Flags = append(out.Flags, schemaFlag{
			Name:        f.Name,
			Shorthand:   f.Shorthand,
			Description: f.Usage,
			Type:        f.Value.Type(),
			Default:     f.DefValue,
		})
	})
	for _, sub := range c.Commands() {
		if sub.Hidden || sub.Name() == "help" {
			continue
		}
		out.Children = append(out.Children, walkCommand(sub, path))
	}
	return out
}

func exitCodeSchema() []map[string]any {
	return []map[string]any{
		{"code": ExitOK, "name": "ok", "meaning": "success"},
		{"code": ExitGeneric, "name": "generic", "meaning": "execution failure (network, server error, unknown)"},
		{"code": ExitUsage, "name": "usage", "meaning": "bad flag, missing arg, command misuse"},
		{"code": ExitUnauthorized, "name": "unauthorized", "meaning": "no token or token rejected; run `pipe2 auth login`"},
		{"code": ExitNotFound, "name": "not_found", "meaning": "resource (pipeline, run, asset) does not exist"},
		{"code": ExitForbidden, "name": "forbidden", "meaning": "token is valid but lacks permission"},
	}
}
