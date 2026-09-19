# Pipe2.ai CLI

`pipe2` is the official Pipe2.ai command-line tool. Every command supports
machine-readable output for agent workflows.

## Precheck

Run `pipe2 --version` first. If it errors with "command not found",
stop and ask the human to install it — one of:

- `brew install pipe2-ai/tap/pipe2` (macOS)
- `go install github.com/pipe2-ai/pipe2-cli/cmd/pipe2@latest`
- Download a release binary from https://github.com/pipe2-ai/pipe2-cli/releases

Do not try to install it yourself.

## Setup (once per machine)

1. Mint a Personal Access Token at https://pipe2.ai/api-keys
2. `echo $PAT | pipe2 auth login --token -`
3. Verify with `pipe2 auth whoami --json`

Token storage: `$XDG_CONFIG_HOME/pipe2/config.json` (mode 0600). You can
also pass it per-call via `--token` or `$PIPE2_TOKEN`.

**Preconfigured unattended sessions:** when the task supplies `PIPE2_API_URL`
and `PIPE2_TOKEN`, skip interactive setup and use that connection. Keep the
token out of command arguments and output. Report missing access or a missing
CLI; do not log in, replace credentials, change the endpoint, or request
broader access. Discover pipelines and schemas as below, and call only those
approved for the task. A cyclic pipeline call rejected by the server must not
be retried through another chain.

If the task provides a command-scoped credential, dispatch with `--wait` in the
same command; starting a run and waiting in a later command is unsupported.
Use the command's stated time limit for `--wait-timeout`; a command that ends
before its pipeline finishes cancels that child run.

In a pipeline workspace, discovery returns the real approved pipeline's current
input and output schemas, model options and estimates. Use supplied asset IDs
in the fields described by that schema. Choose whether the pipeline is useful
and select its parameters from the task and current schema; do not assume a
particular transcription provider, model or special input shape.

Child pipeline charges count toward the parent run's displayed maximum.
The real pipeline output and its asset records appear in `final`. After the
command completes, its output assets are available locally in
`pipeline-runs/<run_id>/assets/<asset_id>`. Read
`pipeline-runs/<run_id>/manifest.json` in the next command for the output and
asset `workspace_path` values. This avoids downloading public URLs from an
isolated workspace. Only assets produced by the approved child call are staged.

Paid dispatches are not automatically safe to retry. If a run ID was returned,
inspect or wait on that run before submitting another. After a failure, correct
its cause and check the task's remaining budget before retrying; stop and report
an unresolved failure or ambiguous dispatch outcome.

## Agent contract

- **Always use `--json`** (or rely on TTY auto-detection — when stdout is
  not a TTY, JSON is the default).
- **Stdout is data, stderr is logs.** Pipe stdout into `jq` or `json.loads`;
  stderr is safe to discard or surface to the human.
- **Exit codes encode failure class.** Branch on them instead of parsing
  error strings:
  - `0` ok
  - `1` generic failure (network, server)
  - `2` usage error (bad flag/arg)
  - `3` unauthorized — re-run `pipe2 auth login` for interactive setup;
    stop and report the failure in a preconfigured unattended session
  - `4` not found
  - `5` forbidden
- **Schema is the source of truth.** Run `pipe2 schema --json` once per
  session; the output lists every command, flag, type, default, and exit
  code. Treat it like an OpenAPI spec.
- **Idempotency.** `pipe2 auth login`, `pipe2 skill install`, and
  `pipe2 assets delete` are safe to retry.

## Common workflows

### Calling a pipeline end-to-end

Every agent run follows the same three-step shape: **discover → inspect
input schema → dispatch**. Don't hardcode pipeline slugs or input
shapes — read them from the live catalog so your script keeps working
when new pipelines are added or existing ones change.

```bash
# 1. Discover: list a page of available pipelines
pipe2 pipelines list --page 1 --limit 20 --json \
  | jq -r '.[] | "\(.slug)\t\(.name)\t\(.description)"'

# 2. Inspect the full record for the approved pipeline you picked:
pipe2 pipelines get video-generator --json > pipeline.json
jq '.input_schema' pipeline.json

#    This record includes output_schema, models, hints, and ui_schema.
#    Use `pipelines estimate` with your input for a credit estimate.

# 3. Build a JSON payload that satisfies the schema, then dispatch:
pipe2 pipelines run \
  --pipeline video-generator \
  --input-json '{"prompt":"a corgi surfing"}' \
  --wait --wait-timeout 15m --json
```

Returns a JSON object with `run` (the dispatch result) and `final` (the
terminal pipeline_runs row). Exit code is non-zero if the run failed or
timed out, even with `--json`.

**Tips:**
- Lists are paginated. Advance `--page` until you find an appropriate pipeline
  or receive fewer rows than `--limit`.
- To send a large payload, write it to a file and pass `--input ./in.json`
  (or pipe stdin with `--input -`).
- Validate your payload against `input_schema` locally (e.g. with `ajv`,
  `jsonschema`, or any JSON Schema validator) before dispatching. The
  server will reject invalid inputs with exit code 2, but local validation
  gives you a better error and avoids a round-trip.

### Stream-friendly listing

```bash
pipe2 pipelines list --json | jq -r '.[].slug'
pipe2 runs list --json     | jq -r '.[] | "\(.id) \(.status)"'
```

### Inspect a single run

```bash
pipe2 runs get $RUN_ID --json
pipe2 runs wait $RUN_ID --timeout 5m --json
```

### Manage assets

```bash
pipe2 assets upload ./local-video.mp4 --tags raw --json
pipe2 assets list --json
pipe2 assets delete $ASSET_ID --json
```

`upload` registers a local file (image / video / audio) as an asset and
returns its `id` and public `url`, both of which any pipeline accepts as
input. Size limits: 10 MiB images, 5 GiB videos, 1 GiB audio.

Files at or below 25 MiB use a single PUT; larger files automatically
switch to S3 multipart with parallel chunked uploads. Tune the multipart
path with `--parallel <N>` (concurrent part PUTs, default 4) and
`--part-size <MiB>` (chunk size, default 32 MiB). Ctrl-C aborts the
in-progress upload cleanly. Use `--content-type <mime>` to override
extension-based detection.

### Credits

```bash
pipe2 credits balance --json
```

## Discovery

If you don't know which command to use, ask the CLI itself — never guess:

```bash
pipe2 schema --json                  # full tree
pipe2 schema pipelines --json        # subtree
pipe2 schema pipelines run --json    # one command
pipe2 help                           # human-readable
```

## Troubleshooting

- **Exit 3 after `auth login`**: token was rejected. Confirm it's still
  valid in the dashboard; tokens can be revoked there.
- **`pipe2 pipelines run` returns `run.run_id` but `pipe2 runs get` says
  not found**: wait briefly and query that run again, or use `--wait`
  from the start. Do not submit another run to resolve a lookup delay.
- **`pipe2 schema` shows a command but invocation 404s**: the local CLI
  is newer than the prod API. `pipe2 --version` and the API version
  should match major+minor.

---

## Command reference

- [`pipe2 assets`](#pipe2-assets) — Upload, inspect, and delete assets
- [`pipe2 auth`](#pipe2-auth) — Manage Pipe2.ai authentication
- [`pipe2 credits`](#pipe2-credits) — Credit balance and history
- [`pipe2 pipelines`](#pipe2-pipelines) — List, inspect, estimate, and run Pipe2.ai pipelines
- [`pipe2 recipe`](#pipe2-recipe) — Run cookbook recipes
- [`pipe2 runs`](#pipe2-runs) — Inspect pipeline runs
- [`pipe2 schema`](#pipe2-schema) — Dump a machine-readable schema of every command, flag, and exit code
- [`pipe2 skill`](#pipe2-skill) — Install and inspect the bundled Claude Code skill

### `pipe2 assets`

<!-- anchor: pipe2-assets -->

Upload, inspect, and delete assets

```
pipe2 assets
```

### `pipe2 assets delete`

<!-- anchor: pipe2-assets-delete -->

Delete an asset (DB row + S3 object)

```
pipe2 assets delete <asset-id>
```

### `pipe2 assets list`

<!-- anchor: pipe2-assets-list -->

List your assets

```
pipe2 assets list
```

### `pipe2 assets upload`

<!-- anchor: pipe2-assets-upload -->

Upload a local file as an asset (image/video/audio)

Upload a local file and register it as an asset in your library.

The asset is stored in Pipe2.ai's S3 bucket and gets a public URL you can
pass to any pipeline (e.g. video-trim, transcription, captions) via either
the asset's id or its url field.

Files at or below 25 MiB use a single PUT. Larger files automatically use
S3 multipart with parallel chunked PUTs (--parallel chunks at a time, each
--part-size MiB). On Ctrl-C the in-progress upload is aborted cleanly.

Size limits: 10 MiB images, 5 GiB videos, 1 GiB audio.

Examples:
  pipe2 assets upload ./interview.mp4
  pipe2 assets upload ./voiceover.wav --tags podcast,episode-42
  pipe2 assets upload ./long-podcast.mp4 --parallel 8 --part-size 64
  pipe2 assets upload ./photo --content-type image/jpeg

```
pipe2 assets upload <file> [flags]
```

**Flags:**

- `--content-type` (`string`) — explicit MIME type (default: detected from extension)
- `--parallel` (`int`) default `0` — concurrent part uploads in multipart mode (default 4)
- `--part-size` (`int64`) default `0` — multipart chunk size in MiB (default 32, min 5)
- `--tags` (`stringSlice`) default `[]` — comma-separated tags to attach to the asset

### `pipe2 auth`

<!-- anchor: pipe2-auth -->

Manage Pipe2.ai authentication

```
pipe2 auth
```

### `pipe2 auth login`

<!-- anchor: pipe2-auth-login -->

Save a personal access token to the config file

Save a Pipe2.ai personal access token (PAT) to the config file.

You can mint a token at https://pipe2.ai/api-keys and pass it via --token,
or pipe it on stdin:

  echo $PAT | pipe2 auth login --token -

The token is stored at $XDG_CONFIG_HOME/pipe2/config.json with mode 0600.

```
pipe2 auth login [flags]
```

**Flags:**

- `--api-url` (`string`) — override saved API URL
- `--storage-url` (`string`) — override saved asset-storage base URL
- `--token` (`string`) — personal access token, or "-" to read from stdin

### `pipe2 auth logout`

<!-- anchor: pipe2-auth-logout -->

Clear the saved token

```
pipe2 auth logout
```

### `pipe2 auth whoami`

<!-- anchor: pipe2-auth-whoami -->

Show the user identified by the current token

```
pipe2 auth whoami
```

### `pipe2 credits`

<!-- anchor: pipe2-credits -->

Credit balance and history

```
pipe2 credits
```

### `pipe2 credits balance`

<!-- anchor: pipe2-credits-balance -->

Show current credit balance

```
pipe2 credits balance
```

### `pipe2 pipelines`

<!-- anchor: pipe2-pipelines -->

List, inspect, estimate, and run Pipe2.ai pipelines

```
pipe2 pipelines
```

### `pipe2 pipelines estimate`

<!-- anchor: pipe2-pipelines-estimate -->

Estimate pipeline credit cost

Estimate the credit reservation and expected charge for a pipeline input without dispatching a run.

Examples:
  pipe2 pipelines estimate --pipeline video-generator --input ./input.json
  pipe2 pipelines estimate --pipeline video-generator --input-json '{"prompt":"a cat"}'

```
pipe2 pipelines estimate [flags]
```

**Flags:**

- `--input` (`string`) — path to JSON input file, or "-" for stdin
- `--input-json` (`string`) — inline JSON input
- `--pipeline` (`string`) — pipeline slug (required)

### `pipe2 pipelines get`

<!-- anchor: pipe2-pipelines-get -->

Show a pipeline's input and output schemas

```
pipe2 pipelines get <slug>
```

### `pipe2 pipelines list`

<!-- anchor: pipe2-pipelines-list -->

List available pipelines

```
pipe2 pipelines list [flags]
```

**Flags:**

- `--limit` (`int`) default `20` — number of pipelines per page
- `--page` (`int`) default `1` — page number (1-based)

### `pipe2 pipelines run`

<!-- anchor: pipe2-pipelines-run -->

Dispatch a pipeline run

Dispatch a pipeline run with the given JSON input.

Examples:
  pipe2 pipelines run --pipeline video-generator --input ./input.json
  echo '{"prompt":"a cat"}' | pipe2 pipelines run --pipeline video-generator --input -
  pipe2 pipelines run --pipeline video-generator --input-json '{"prompt":"a cat"}' --wait

```
pipe2 pipelines run [flags]
```

**Flags:**

- `--input` (`string`) — path to JSON input file, or "-" for stdin
- `--input-json` (`string`) — inline JSON input
- `--pipeline` (`string`) — pipeline slug (required)
- `--wait` (`bool`) — block until the run reaches a terminal status
- `--wait-timeout` (`duration`) default `10m0s` — max time to wait when --wait is set

### `pipe2 recipe`

<!-- anchor: pipe2-recipe -->

Run cookbook recipes

Recipes are typed Go programs that orchestrate one or more Pipe2
pipelines. They ship with the CLI binary.

Examples:
  pipe2 recipe list
  pipe2 recipe info clip-factory
  pipe2 recipe run clip-factory --input https://example.com/talk.mp4 --reformat 9:16
  pipe2 recipe run clip-factory --input ./my-clip.mp4 --highlights-count 3 --preset karaoke-gradient

```
pipe2 recipe
```

### `pipe2 recipe download`

<!-- anchor: pipe2-recipe-download -->

Download every step artifact from a --capture-to run

Download the per-step artifacts recorded in a recipe run's state.json.

A run started with --capture-to <dir> writes a state.json recording
each step's pipeline, run id, and output. download reads that file and
fetches every step's artifact:

  pipe2 recipe run clip-factory --input clip.mp4 --capture-to ./out
  pipe2 recipe download --from ./out

Files land as step-<n>-<pipeline>.<ext>. Use --to to write elsewhere.

Asset paths are resolved against the configured storage base — set it
once with 'pipe2 auth login --storage-url ...' or per-call via
$PIPE2_STORAGE_URL.

```
pipe2 recipe download [flags]
```

**Flags:**

- `--from` (`string`) — capture directory containing state.json (required)
- `--to` (`string`) — output directory (default: same as --from)

### `pipe2 recipe info`

<!-- anchor: pipe2-recipe-info -->

Pretty-print a recipe's manifest

```
pipe2 recipe info <slug>
```

### `pipe2 recipe list`

<!-- anchor: pipe2-recipe-list -->

List recipes compiled into this binary

```
pipe2 recipe list
```

### `pipe2 recipe run`

<!-- anchor: pipe2-recipe-run -->

Execute a recipe end-to-end

```
pipe2 recipe run <slug> [--<input> <value> ...] [flags]
```

**Flags:**

- `--asset` (`string`) — shortcut for an already-uploaded source asset: equivalent to setting the recipe's `source` input to <id-or-url> with --no-fetch
- `--capture-to` (`string`) — directory where Capture writes per-step artifacts and the final hero output
- `--cookies` (`string`) — path to a Netscape cookies.txt file for the client-side yt-dlp fetch (yt-dlp --cookies); the headless/CI alternative to --cookies-from-browser
- `--cookies-from-browser` (`string`) — browser to load cookies from for the client-side yt-dlp fetch of a remote --input (yt-dlp --cookies-from-browser) — the fix for YouTube's "Sign in to confirm you're not a bot" wall. e.g. chrome, firefox, safari, edge, or chrome:Default
- `--dry-run` (`bool`) — resolve inputs and log the chain that would run, but skip dispatch (no credits charged, no auth required)
- `--estimate` (`bool`) — fetch credit cost for each step via the API and print a running total (composes with --dry-run for a no-spend cost preview; requires auth)
- `--no-fetch` (`bool`) — treat every source input as an already-uploaded asset reference (URL / id / /s3 path) and pass it through verbatim — skip the client-side download + upload of remote URLs
- `--resume` (`bool`) — reuse step outputs recorded in <capture-to>/state.json from a prior run; only steps not yet recorded are dispatched
- `--stop-before-step` (`int`) default `0` — pause before this numbered pipeline call; inspect captures, then continue with --resume
- `--ytdlp-extractor-args` (`stringArray`) default `[]` — repeatable yt-dlp --extractor-args value for the remote --input fetch (e.g. youtube:player_client=tv); power-user escape hatch

### `pipe2 runs`

<!-- anchor: pipe2-runs -->

Inspect pipeline runs

```
pipe2 runs
```

### `pipe2 runs get`

<!-- anchor: pipe2-runs-get -->

Show a single run by id

```
pipe2 runs get <run-id>
```

### `pipe2 runs list`

<!-- anchor: pipe2-runs-list -->

List recent pipeline runs

```
pipe2 runs list
```

### `pipe2 runs wait`

<!-- anchor: pipe2-runs-wait -->

Block until the run reaches a terminal status

```
pipe2 runs wait <run-id> [flags]
```

**Flags:**

- `--timeout` (`duration`) default `10m0s` — max time to wait

### `pipe2 schema`

<!-- anchor: pipe2-schema -->

Dump a machine-readable schema of every command, flag, and exit code

Print a JSON schema of the CLI command tree.

Agents should call this once at the start of a session and treat the output
as the canonical source of truth for what commands exist, what flags they
take, and what exit codes mean. Calling with a command path narrows the
output to that subtree:

  pipe2 schema                 # full tree
  pipe2 schema pipelines       # just the pipelines subtree
  pipe2 schema pipelines run   # one command

```
pipe2 schema [command-path...]
```

### `pipe2 skill`

<!-- anchor: pipe2-skill -->

Install and inspect the bundled Claude Code skill

```
pipe2 skill
```

### `pipe2 skill install`

<!-- anchor: pipe2-skill-install -->

Write the bundled SKILL.md to ~/.claude/skills/pipe2-cli/

Install the bundled Pipe2.ai skill so Claude Code (or any compatible
agent runtime) can discover the CLI as a tool.

The skill is embedded inside the binary, so this command works offline and
always installs the version that matches the CLI you're running.

```
pipe2 skill install [flags]
```

**Flags:**

- `--dest` (`string`) — override install directory (default: ~/.claude/skills/pipe2-cli)

### `pipe2 skill show`

<!-- anchor: pipe2-skill-show -->

Print the bundled SKILL.md to stdout

```
pipe2 skill show
```
