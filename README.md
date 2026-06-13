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

## Recipes

Recipes are typed multi-step pipelines compiled into the binary — one
command runs a whole chain (e.g. long-form video → captioned shorts).

```bash
pipe2 recipe list                       # recipes shipped in this binary
pipe2 recipe info clip-factory          # manifest: chain, inputs, samples
pipe2 recipe run clip-factory --input ./talk.mp4 --reformat 9:16
pipe2 recipe run clip-factory --input https://youtube.com/watch?v=… --highlights-count 3
pipe2 recipe run clip-factory --input ./talk.mp4 --dry-run --estimate
```

### Source media (`--input`)

The `--input` of a media recipe can be a **local file**, a **direct media
URL**, a **streaming/social URL** (YouTube, Vimeo, TikTok, …), or an
**existing pipe2 asset** (its URL, `/s3/...` path, or id). The platform
never fetches external URLs server-side, so the CLI resolves a remote
`--input` **on your machine**: it downloads the media (yt-dlp for
streaming/social, plain HTTP for direct links), uploads it as a pipe2
asset, and hands the recipe only the asset URL. A failed fetch (a 403, or
an HTML page where media was expected) stops with a clear
`source fetch failed` error rather than a confusing downstream probe error.

- **`yt-dlp` + `ffmpeg` power the streaming/social path** and are
  **auto-installed on first use** — a checksum-verified download into the
  go-ytdlp cache (`$XDG_CACHE_HOME/go-ytdlp`, or the OS user-cache
  equivalent). Direct media URLs and local files don't need them.
  - First-run bootstrap needs network access to GitHub. Offline / sandboxed?
    Install yt-dlp + ffmpeg yourself and set `PIPE2_YTDLP_SYSTEM=1` to use
    the ones on `PATH`, or `PIPE2_YTDLP_NO_DOWNLOAD=1` to require a system
    install and skip the download. A failed bootstrap surfaces an actionable
    error, not a stack trace.
  - The yt-dlp version is pinned (and checksum-verified) by the bundled
    go-ytdlp release; `PIPE2_YTDLP_SYSTEM=1` opts into whatever (newer)
    yt-dlp you have installed instead.
- Already uploaded the source? Skip the download/upload with
  `--asset <id>` (or `--no-fetch`, which passes the source input through to
  the backend verbatim).

Add `--capture-to ./out` to save every step's artifact, then
`pipe2 recipe download --from ./out` to pull them locally. Asset paths
resolve against the storage base — set it once with
`pipe2 auth login --storage-url ...` or per-call via `$PIPE2_STORAGE_URL`.

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

| Source     | Variable / flag                                                  |
|------------|------------------------------------------------------------------|
| flag       | `--token`, `--api-url`, `--storage-url`, `--config`              |
| env        | `PIPE2_TOKEN`, `PIPE2_API_URL`, `PIPE2_STORAGE_URL`, `XDG_CONFIG_HOME` |
| file       | `$XDG_CONFIG_HOME/pipe2/config.json` (mode 0600)                 |

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
