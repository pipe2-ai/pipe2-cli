// Package pipe2cli provides module-root utilities for the pipe2 CLI.
//
// Its only current job is to host the canonical SKILL.md via go:embed.
// Because go:embed paths cannot use `..`, the Go file declaring the
// embed directive has to live in (or above) the directory containing
// the embedded file. We keep the canonical skill at
// `.claude/skills/pipe2-cli/SKILL.md` — the conventional path both
// `npx skills add` and `/plugin marketplace add` look for after the
// subtree-split public repo is published — and this root-level file
// reaches down into it. One source of truth; no mirrored copy.
package pipe2cli

import _ "embed"

//go:embed .claude/skills/pipe2-cli/SKILL.md
var SkillMD []byte
