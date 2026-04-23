# pipe2-cli

Official command-line tool for [Pipe2.ai](https://pipe2.ai), designed for
both human developers and AI agents.

## Why a CLI built for agents?

Every command:

- Emits **JSON to stdout** (auto when stdout is not a TTY, or via `--json`)
- Sends **human progress to stderr** so agents can pipe stdout cleanly
- Returns **typed exit codes** (`3` = unauthorized, `4` = not found, etc.)
  so shell scripts can branch on failure class instead of parsing strings
- Is fully introspectable via `pipe2 schema --json` — agents pull the
  command tree, flag types, and exit codes from the binary itself

## Install

```bash
# Homebrew (macOS)
brew install pipe2-ai/tap/pipe2

# Go
go install github.com/pipe2-ai/pipe2-cli/cmd/pipe2@latest
```

Or grab a release binary from
[github.com/pipe2-ai/pipe2-cli/releases](https://github.com/pipe2-ai/pipe2-cli/releases).

## First run

```bash
# 1. Mint a Personal Access Token at https://pipe2.ai/api-keys
# 2. Save it
echo $PAT | pipe2 auth login --token -

# 3. Confirm
pipe2 auth whoami --json
```

## Common commands

```bash
pipe2 pipelines list --json
pipe2 pipelines run --pipeline video-generator --input ./input.json --wait
pipe2 runs list --json
pipe2 runs get $RUN_ID --json
pipe2 runs wait $RUN_ID --timeout 5m --json
pipe2 assets list --json
pipe2 assets delete $ASSET_ID
pipe2 credits balance --json
```

## Schema introspection

```bash
pipe2 schema --json                # full command tree
pipe2 schema pipelines --json      # subtree
pipe2 schema pipelines run --json  # one command
```

The output is the canonical source of truth for what commands exist, what
flags they take, and what exit codes mean.

## Bundled Claude Code skill

The CLI ships with a Claude Code skill so any agent runtime can discover
it as a tool. Three install paths:

```bash
# 1. From the CLI itself (embedded, no network):
pipe2 skill install

# 2. Via vercel-labs/skills — reads SKILL.md from GitHub, no binary needed:
npx skills add pipe2-ai/pipe2-cli --skill pipe2-cli

# 3. As a Claude Code plugin marketplace (installs the full plugin):
/plugin marketplace add pipe2-ai/pipe2-cli
```

All three write to `~/.claude/skills/pipe2-cli/SKILL.md` (or the equivalent
directory for other agent runtimes). Path 1 is offline and guaranteed to
match the binary you ran; path 2 is useful in sandboxes where the binary
isn't installed yet; path 3 bundles the skill alongside future MCP servers
and slash commands via the Claude Code plugin system.

## Configuration

| Source     | Variable / flag                                            |
|------------|------------------------------------------------------------|
| flag       | `--token`, `--api-url`, `--config`                         |
| env        | `PIPE2_TOKEN`, `PIPE2_API_URL`, `XDG_CONFIG_HOME`          |
| file       | `$XDG_CONFIG_HOME/pipe2/config.json` (mode 0600)           |

Resolution order: flag > env > file > built-in default.

## Exit codes

| Code | Name           | Meaning                                          |
|------|----------------|--------------------------------------------------|
| 0    | ok             | success                                          |
| 1    | generic        | network / server failure                         |
| 2    | usage          | bad flag / missing arg                           |
| 3    | unauthorized   | no token or token rejected                       |
| 4    | not_found      | resource missing                                 |
| 5    | forbidden      | token valid but insufficient permission          |

## License

MIT
