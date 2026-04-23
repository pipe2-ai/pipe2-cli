package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	pipe2cli "github.com/pipe2-ai/pipe2-cli"
)

// Canonical SKILL.md lives at .claude/skills/pipe2-cli/SKILL.md (the
// conventional path both `npx skills add` and the Claude Code plugin
// marketplace resolve), and is go:embed'd from a root-level package
// because go:embed can't use `..`.
var skillMD = pipe2cli.SkillMD

func newSkillCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "skill",
		Short: "Install and inspect the bundled Claude Code skill",
	}
	c.AddCommand(newSkillInstallCmd(), newSkillShowCmd())
	return c
}

func newSkillInstallCmd() *cobra.Command {
	var dest string
	c := &cobra.Command{
		Use:   "install",
		Short: "Write the bundled SKILL.md to ~/.claude/skills/pipe2-cli/",
		Long: `Install the bundled Pipe2.ai skill so Claude Code (or any compatible
agent runtime) can discover the CLI as a tool.

The skill is embedded inside the binary, so this command works offline and
always installs the version that matches the CLI you're running.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			target := dest
			if target == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return err
				}
				target = filepath.Join(home, ".claude", "skills", "pipe2-cli")
			}
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("mkdir: %w", err)
			}
			path := filepath.Join(target, "SKILL.md")
			if err := os.WriteFile(path, skillMD, 0o644); err != nil {
				return fmt.Errorf("write: %w", err)
			}
			Status("installed skill to %s", path)
			return Out(map[string]any{"path": path, "bytes": len(skillMD)})
		},
	}
	c.Flags().StringVar(&dest, "dest", "", "override install directory (default: ~/.claude/skills/pipe2-cli)")
	return c
}

func newSkillShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the bundled SKILL.md to stdout",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := os.Stdout.Write(skillMD)
			return err
		},
	}
}
