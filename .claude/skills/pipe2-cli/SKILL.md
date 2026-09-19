---
name: pipe2-cli
description: Use when interacting with Pipe2.ai pipelines, runs, assets, or credits from the command line — listing/dispatching pipelines, inspecting run status, downloading or deleting assets, or checking credit balance. Triggers on any task that needs to call the Pipe2.ai API as a developer or background agent.
---

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
