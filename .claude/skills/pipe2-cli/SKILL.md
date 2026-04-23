---
name: pipe2-cli
description: Use when interacting with Pipe2.ai pipelines, runs, assets, or credits from the command line — listing/dispatching pipelines, inspecting run status, downloading or deleting assets, or checking credit balance. Triggers on any task that needs to call the Pipe2.ai API as a developer or background agent.
---

# Pipe2.ai CLI

`pipe2` is the official Pipe2.ai command-line tool. Every command supports
machine-readable output and is safe to invoke from an agent loop.

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
  - `3` unauthorized — re-run `pipe2 auth login`
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
# 1. Discover: list every active pipeline with slug, name, description
pipe2 pipelines list --json | jq -r '.[] | "\(.slug)\t\(.name)\t\(.description)"'

# 2. Inspect the input schema for the one you picked. `pipelines list`
#    already embeds `input_schema` (JSON Schema draft-07) per pipeline —
#    no separate call needed:
pipe2 pipelines list --json \
  | jq '.[] | select(.slug=="video-generator") | .input_schema'

#    The same row also carries `output_schema`, `pricing` (millicredits),
#    `required_providers`, and `ui_schema` (for form rendering). Cache
#    the list for a few minutes and reuse it across calls.

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
pipe2 assets list --json
pipe2 assets delete $ASSET_ID --json
```

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
  not found**: the API and worker are healthy but the row hasn't been
  written yet — wait 1-2s and retry, or use `--wait` from the start.
- **`pipe2 schema` shows a command but invocation 404s**: the local CLI
  is newer than the prod API. `pipe2 --version` and the API version
  should match major+minor.
