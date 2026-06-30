package cookbook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Khan/genqlient/graphql"
	pipe2 "github.com/pipe2-ai/sdk-go"
)

// Client is the Pipe2 API surface a recipe needs. Modeled as an
// interface (not a concrete type) so tests can inject a mock without
// pulling in the GraphQL transport.
type Client interface {
	RunPipeline(ctx context.Context, slug string, input json.RawMessage) (runID string, err error)
	WaitRun(ctx context.Context, runID string, timeout time.Duration) (*RunRow, error)
	UploadAsset(ctx context.Context, localPath string) (url string, err error)
	// EstimatePipelineCost asks the server "what would you charge
	// for this slug + input." Used by `recipe run --estimate` so the
	// CLI can show per-step cost without porting the billing math.
	// Returns the same shape as the EstimatePipelineCost GraphQL
	// action so the wire format stays single-sourced.
	EstimatePipelineCost(ctx context.Context, slug string, input json.RawMessage) (*PipelineCostEstimate, error)
}

// PipelineCostEstimate mirrors the GraphQL action response. ReservationMC
// is what gets held on dispatch (with the safety buffer for metered);
// EstimatedMC is the buffer-free estimate of actual post-confirm charge.
// IsMetered tells callers whether the two can diverge.
type PipelineCostEstimate struct {
	PipelineSlug  string
	ReservationMC int
	EstimatedMC   int
	IsMetered     bool
}

// RunRow captures everything the runtime needs from a finished
// pipeline run. Status is one of "completed" / "failed" / "cancelled" /
// "timed_out"; Output is the pipeline's structured result. Trim of
// the SDK's GetPipelineRunPipeline_runs_by_pkPipeline_runs.
type RunRow struct {
	ID             string
	Status         string
	Output         map[string]any
	ErrorMessage   string
	CreditsCharged *int
}

// Context is what each Recipe.Run receives. Provides typed input
// access, pipeline invocation, output capture, and progress logging.
// Substep returns a child Context for fan-out (e.g. one per moment
// in podcast-clip-factory), giving each branch its own capture
// numbering and log prefix.
type Context struct {
	ctx    context.Context
	client Client

	// Inputs is the user's resolved values, keyed by Input.Name.
	// Type-asserted via the Values methods.
	Inputs Values

	stdout, stderr io.Writer

	// captureDir is set when the runner was invoked with --capture-to
	// (or the script set $PIPE2_RECIPE_OUT_DIR). Empty disables capture.
	captureDir string

	// substepPrefix is non-empty for child contexts spawned via
	// Substep — the prefix labels their log lines and step files.
	substepPrefix string

	// uploadCache memoizes Values.AssetURL upload calls so the same
	// local path passed for two different inputs isn't uploaded twice
	// in the same run.
	uploadCache map[string]string

	// finalOutput is set by SetOutput; the runner echoes it on success.
	finalOutput string

	// stepCounter is incremented on every RunPipeline call so we can
	// number steps for state.json + resume. Substep contexts share the
	// parent's counter (fan-out branches each get their own through
	// substepPrefix instead).
	stepCounter int

	// chain mirrors Manifest.Chain. When set, RunPipeline logs
	// `[N/M] slug ▸ what-it-does` so users can read the pipeline
	// graph from the run output instead of memorising the recipe.
	// The slug→WhatItDoes lookup is by first-match; recipes with
	// multiple identical pipeline slugs in their chain (e.g. two
	// image-generator calls) will reuse the same description.
	chain []ChainStep

	// dryRun short-circuits dispatch. RunPipeline logs what it would
	// do, returns a stub Result with synthetic URLs, and never hits
	// the API. Used by `pipe2 recipe run --dry-run` so users can
	// sanity-check a 30-credit chain before spending credits.
	dryRun bool

	// estimate, when true, makes RunPipeline call the
	// estimate_pipeline_cost GraphQL action for every step (in both
	// dry and live mode) and accumulate the totals below. The recipe
	// runner prints the tally at the end. Composes with dryRun: a
	// dry-run estimate is the "what will this cost before I burn
	// credits" preview; a live-run estimate annotates each step's
	// charge alongside the live API call.
	estimate            bool
	estimateReservation int
	estimateActual      int
	estimateUnavailable int // count of steps where the estimate call failed

	// resumeState, when non-nil, carries the previous run's recorded
	// step outputs. RunPipeline checks against this before dispatching
	// and short-circuits with the cached Result when the slug at the
	// current stepCounter matches.
	resumeState *ResumeState

	// stateFile is the absolute path to <captureDir>/state.json. Empty
	// when captureDir is empty (no state to persist).
	stateFile string

	// stateMu serialises the read-modify-write of state.json. It is a
	// pointer so Substep/WithContext shallow-copies share ONE lock —
	// fan-out branches recordStep concurrently into the same file.
	stateMu *sync.Mutex

	// recipeSlug is needed in state.json so resuming detects when the
	// user accidentally points --resume at the wrong recipe's state.
	recipeSlug string

	// storageURL is the asset-storage base. Pipeline outputs come back
	// as "/s3/..." paths; Capture resolves them against this before
	// downloading. Empty disables resolution — a relative URL then
	// fails the fetch loudly instead of guessing a host. It also tells
	// ResolveSourceURL which http(s) inputs are our own already-uploaded
	// assets (same host) versus third-party URLs to fetch.
	storageURL string

	// noFetch is the --no-fetch / --asset escape hatch. When true,
	// ResolveSourceURL treats the source as an asset reference the caller
	// has already uploaded and passes it through verbatim — no client-side
	// download, no upload.
	noFetch bool

	// fetcher resolves a remote source URL to a local temp file. Defaults
	// to fetchRemoteSource (yt-dlp / plain HTTP); tests inject a stub via
	// WithFetcher so source resolution doesn't touch the network.
	fetcher remoteFetcher

	// fetchOpts tunes the default remote fetcher's yt-dlp path (cookies /
	// extractor args) — set from `pipe2 recipe run --cookies-from-browser`
	// et al. Only applies when fetcher is nil (the production default); a
	// test-injected WithFetcher stub ignores it.
	fetchOpts SourceFetchOptions
}

// remoteFetcher downloads rawURL to a local temp file and returns the path
// plus a cleanup func (always non-nil, safe to call on error). It is the
// seam ResolveSourceURL uses for the remote-URL path; fetchRemoteSource is
// the production implementation.
type remoteFetcher func(ctx context.Context, rawURL string, progress progressFunc) (path string, cleanup func(), err error)

// ResumeState is the on-disk form of recipe progress. Written to
// <captureDir>/state.json after every successful pipeline dispatch.
// Reading + writing is best-effort — a missing or corrupt state.json
// just means the recipe runs from scratch.
type ResumeState struct {
	Recipe string       `json:"recipe"`
	Steps  []ResumeStep `json:"steps"`
}

// ResumeStep records one completed pipeline dispatch. RunID lets the
// user trace what was reused; Output is the structured pipeline output
// the recipe consumed downstream.
//
// Substep is the fan-out branch's prefix (e.g. "substep-2"), empty for the
// main chain. It is part of the resume key: in a fan-out recipe every clip
// runs the same step Idx, so keying on Idx alone made every branch reuse the
// first branch's run on --resume (and clobber each other's state.json entry).
type ResumeStep struct {
	Idx      int            `json:"idx"`
	Substep  string         `json:"substep,omitempty"`
	Pipeline string         `json:"pipeline"`
	RunID    string         `json:"run_id"`
	Output   map[string]any `json:"output"`
}

// NewContext constructs a Context from validated inputs and a live
// client. The CLI's `pipe2 recipe run` builds one of these per
// invocation; tests build one with NewTestContext.
func NewContext(ctx context.Context, client Client, inputs map[string]any, opts ...ContextOption) *Context {
	c := &Context{
		ctx:         ctx,
		client:      client,
		Inputs:      Values(inputs),
		stdout:      os.Stdout,
		stderr:      os.Stderr,
		uploadCache: map[string]string{},
		stateMu:     &sync.Mutex{},
	}
	for _, o := range opts {
		o(c)
	}
	if c.captureDir != "" {
		c.stateFile = filepath.Join(c.captureDir, "state.json")
	}
	return c
}

// ContextOption configures NewContext. Optional knobs only — anything
// load-bearing goes through the constructor's required parameters.
type ContextOption func(*Context)

func WithCaptureDir(dir string) ContextOption { return func(c *Context) { c.captureDir = dir } }
func WithStdout(w io.Writer) ContextOption    { return func(c *Context) { c.stdout = w } }
func WithStderr(w io.Writer) ContextOption    { return func(c *Context) { c.stderr = w } }

// WithStorageURL sets the asset-storage base so Capture can resolve
// "/s3/..." pipeline-output paths into fetchable URLs.
func WithStorageURL(url string) ContextOption { return func(c *Context) { c.storageURL = url } }

// WithNoFetch enables the --no-fetch escape hatch: ResolveSourceURL treats
// the source as an already-uploaded asset reference and passes it through
// verbatim, skipping the client-side download + upload. Used by
// `pipe2 recipe run --no-fetch` and `--asset <id>`.
func WithNoFetch(on bool) ContextOption { return func(c *Context) { c.noFetch = on } }

// WithFetcher overrides the remote-source fetcher used by ResolveSourceURL.
// Production code leaves this unset (the default yt-dlp / HTTP fetcher
// applies); tests inject a stub so source resolution never touches the
// network.
func WithFetcher(f remoteFetcher) ContextOption { return func(c *Context) { c.fetcher = f } }

// WithSourceFetchOptions tunes the default remote fetcher's yt-dlp path —
// cookies (--cookies-from-browser / --cookies) and extractor args. These let
// a user past YouTube's "confirm you're not a bot" wall on a flagged IP. They
// apply only to the production fetcher; a WithFetcher stub ignores them.
func WithSourceFetchOptions(opts SourceFetchOptions) ContextOption {
	return func(c *Context) { c.fetchOpts = opts }
}

// WithResume enables resume mode for the context. The runner loads
// state.json from captureDir (must be set) and feeds it in; subsequent
// RunPipeline calls short-circuit when their step+slug match a recorded
// entry. Pass nil to explicitly disable resume even if a state.json
// exists on disk (the runner writes the new file from scratch).
func WithResume(state *ResumeState) ContextOption {
	return func(c *Context) { c.resumeState = state }
}

// WithRecipeSlug stamps the recipe slug into state.json so resume can
// detect mismatched recipe directories ("oh, that state.json is from a
// different recipe"). The runner sets this from the recipe being run.
func WithRecipeSlug(slug string) ContextOption {
	return func(c *Context) { c.recipeSlug = slug }
}

// WithChain wires the Manifest's declared chain into the runtime so
// RunPipeline can render `[N/M] slug ▸ what-it-does` progress lines.
// The runner passes m.Chain on every invocation; recipes don't need
// to touch this.
func WithChain(chain []ChainStep) ContextOption {
	return func(c *Context) { c.chain = chain }
}

// WithDryRun puts the context in dry-run mode: RunPipeline logs the
// planned step but skips dispatch and returns a stub Result whose
// URL(key) values are synthetic dry:// placeholders. Downstream steps
// receive those placeholders and continue logging without making API
// calls or spending credits. Used by `pipe2 recipe run --dry-run`.
func WithDryRun(dry bool) ContextOption {
	return func(c *Context) { c.dryRun = dry }
}

// WithEstimate enables per-step credit cost lookups via the
// estimate_pipeline_cost GraphQL action. Composes with WithDryRun for
// a no-spend preview, or runs alongside live dispatch to surface
// per-step charges as they accrue. Cost-fetch failures degrade
// gracefully (logged, not fatal) so a flaky network never blocks a
// recipe. Used by `pipe2 recipe run --estimate`.
func WithEstimate(on bool) ContextOption {
	return func(c *Context) { c.estimate = on }
}

// Ctx returns the raw context.Context. Recipes use it for cancellation
// or to pass to other Go libraries (errgroup, http.Client, …).
func (c *Context) Ctx() context.Context { return c.ctx }

// stepHeader renders the `[N/M] slug ▸ what-it-does` log prefix used
// by RunPipeline. Total (M) and the description fall back gracefully
// when WithChain wasn't supplied (e.g. test contexts).
func (c *Context) stepHeader(idx int, slug string, overrideDesc ...string) string {
	total := len(c.chain)
	header := slug
	switch {
	case total == 0:
		// no manifest chain wired — bare slug
	case idx > total:
		// Optional steps not declared in Manifest.Chain (e.g.
		// dance-reel's --persona pre-step) overflow the total; drop
		// the /M suffix so we don't render a misleading [8/7].
		header = fmt.Sprintf("[%d] %s", idx, slug)
	default:
		header = fmt.Sprintf("[%d/%d] %s", idx, total, slug)
	}
	// Per-call override wins so conditional / repeated steps (e.g.
	// dance-reel calling image-generator for persona THEN grid) get
	// honest descriptions rather than collapsing onto the manifest's
	// single chain entry.
	if len(overrideDesc) > 0 && overrideDesc[0] != "" {
		return header + " ▸ " + overrideDesc[0]
	}
	for _, step := range c.chain {
		if step.Pipeline == slug && step.WhatItDoes != "" {
			return header + " ▸ " + step.WhatItDoes
		}
	}
	return header
}

// formatEstimateSuffix renders the per-step cost annotation for the
// `[N/M] slug ▸ what-it-does` header when --estimate is on. Returns
// the empty string when no estimate is available so the header reads
// unchanged in plain mode. For metered steps where reservation and
// actual differ, shows both: "  ~12 cr (hold up to 18 cr)".
func formatEstimateSuffix(est *PipelineCostEstimate) string {
	if est == nil {
		return ""
	}
	actual := formatCreditsMC(est.EstimatedMC)
	if est.IsMetered && est.ReservationMC > est.EstimatedMC {
		return fmt.Sprintf("  ~%s cr (hold up to %s cr)", actual, formatCreditsMC(est.ReservationMC))
	}
	return fmt.Sprintf("  %s cr", actual)
}

// formatCreditsMC renders millicredits as a compact credit string —
// 3000 → "3", 1500 → "1.5", 23500 → "23.5". Used by the estimate
// suffix so the running log stays scannable.
func formatCreditsMC(mc int) string {
	c := float64(mc) / 1000.0
	if c == float64(int(c)) {
		return fmt.Sprintf("%d", int(c))
	}
	return fmt.Sprintf("%.1f", c)
}

// summariseDryInput renders a recipe input value for the dry-run log.
// Strings are truncated, slices are summarised by length, others use
// %v. Keeps dry-run output scannable for long prompts.
func summariseDryInput(v any) string {
	switch x := v.(type) {
	case string:
		if len(x) > 80 {
			return fmt.Sprintf("%q (+%d chars)", x[:80], len(x)-80)
		}
		return fmt.Sprintf("%q", x)
	case []string:
		return fmt.Sprintf("[%d urls]", len(x))
	default:
		return fmt.Sprintf("%v", x)
	}
}

// dryStubOutput populates the common URL keys recipes consume so a
// dry-run can flow placeholder URLs from one step to the next. Keys
// not in this map fall back to a generic placeholder via Result.URL.
func dryStubOutput(idx int) map[string]any {
	placeholder := fmt.Sprintf("dry://step-%d", idx)
	return map[string]any{
		"image_url":      placeholder,
		"video_url":      placeholder,
		"audio_url":      placeholder,
		"result_url":     placeholder,
		"transcript":     placeholder,
		"transcript_url": placeholder,
		"subtitle_url":   placeholder,
	}
}

// Logf writes a progress line to stderr with the recipe / substep prefix.
func (c *Context) Logf(format string, args ...any) {
	prefix := ""
	if c.substepPrefix != "" {
		prefix = "[" + c.substepPrefix + "] "
	}
	fmt.Fprintf(c.stderr, "%s%s\n", prefix, fmt.Sprintf(format, args...))
}

// FinalOutput returns whatever the recipe passed to SetOutput, or "".
// Used by the runner and by tests asserting on the recipe's result.
func (c *Context) FinalOutput() string { return c.finalOutput }

// EstimateTotals returns the accumulated cost tallies when WithEstimate
// is on: (reservationMC, estimatedMC, unavailableSteps). The runner
// uses this to print the final cost summary. Always zeros when the
// estimate mode wasn't enabled.
func (c *Context) EstimateTotals() (reservationMC, estimatedMC, unavailable int) {
	return c.estimateReservation, c.estimateActual, c.estimateUnavailable
}

// SetOutput records the recipe's final output URL. The runner prints
// this on success (echoed to stdout).
func (c *Context) SetOutput(url string) { c.finalOutput = url }

// WithContext returns a shallow copy of the Context using the given
// context.Context for cancellation. Used by fan-out branches so an
// errgroup-derived ctx can short-circuit sibling goroutines on the
// first failure.
func (c *Context) WithContext(ctx context.Context) *Context {
	child := *c
	child.ctx = ctx
	return &child
}

// Substep returns a child Context for one branch of fan-out work.
// Steps captured via the child are written as step-<N>-<n>.<ext>
// (parent step, child index). Useful for podcast-clip-factory style
// recipes where the same chain runs N times in parallel.
func (c *Context) Substep(n int) *Context {
	child := *c
	if c.substepPrefix == "" {
		child.substepPrefix = fmt.Sprintf("substep-%d", n)
	} else {
		child.substepPrefix = c.substepPrefix + "/" + fmt.Sprintf("%d", n)
	}
	return &child
}

// RunPipeline dispatches a pipeline, waits for it to reach a terminal
// status, and returns the structured output as a *Result. On any
// non-completed terminal status (failed / cancelled / timed_out) it
// returns a RunError with the worker's error message — the recipe
// can wrap it with extra context if needed.
//
// Default per-step timeout is 15 minutes; override with the
// WithStepTimeout option on RunPipeline if a recipe needs longer.
func (c *Context) RunPipeline(slug string, inputs Inputs, opts ...RunOption) (*Result, error) {
	opt := runOpts{timeout: 15 * time.Minute}
	for _, o := range opts {
		o(&opt)
	}

	c.stepCounter++
	stepIdx := c.stepCounter

	// Resume short-circuit: if we have prior state and the current
	// step+slug match a recorded entry, skip dispatch entirely and
	// return the cached output. Slug mismatch (recipe shape changed)
	// invalidates resume from this step onward — we run live and
	// state.json gets rewritten with the new shape.
	if c.resumeState != nil {
		for _, s := range c.resumeState.Steps {
			// Key on (Idx, Substep): fan-out branches share an Idx, so the
			// substep prefix is what distinguishes clip 1's step from clip 2's.
			if s.Idx == stepIdx && s.Substep == c.substepPrefix {
				if s.Pipeline != slug {
					c.Logf("↻ %s slug changed (%s → %s); running live, state.json will be rewritten",
						c.stepHeader(stepIdx, slug, opt.whatItDoes), s.Pipeline, slug)
					c.resumeState = nil
					break
				}
				c.Logf("↻ %s — reusing run %s from prior recipe execution", c.stepHeader(stepIdx, slug, opt.whatItDoes), s.RunID)
				result := &Result{RunID: s.RunID, Output: s.Output}
				return result, nil
			}
		}
	}

	// Estimate: fetch the per-step credit cost from the server BEFORE
	// dispatch so the log line for each step shows what it will cost.
	// Works in both dry and live modes. Fetch failure is logged and
	// counted but never blocks the step — a flaky network during an
	// estimate run shouldn't abort the recipe.
	var stepEstimate *PipelineCostEstimate
	if c.estimate && c.client != nil {
		if raw, err := json.Marshal(inputs); err == nil {
			if est, err := c.client.EstimatePipelineCost(c.ctx, slug, raw); err != nil {
				c.estimateUnavailable++
				c.Logf("  (estimate unavailable for %s: %v)", slug, err)
			} else {
				stepEstimate = est
				c.estimateReservation += est.ReservationMC
				c.estimateActual += est.EstimatedMC
			}
		}
	}

	// Dry-run: skip dispatch, return a stub Result. Logged with the
	// same step header as a real run so users see the full chain
	// they're about to execute. RunID is `dry-<N>` so downstream
	// `state.json` writes (if any — recordStep is a no-op when
	// captureDir is empty) wouldn't be mistaken for a real run.
	if c.dryRun {
		c.Logf("◌ %s%s — dry-run (no dispatch)",
			c.stepHeader(stepIdx, slug, opt.whatItDoes),
			formatEstimateSuffix(stepEstimate))
		for k, v := range inputs {
			c.Logf("    %s: %s", k, summariseDryInput(v))
		}
		return &Result{
			RunID:  fmt.Sprintf("dry-%d", stepIdx),
			Output: dryStubOutput(stepIdx),
		}, nil
	}

	c.Logf("▸ %s%s — dispatching",
		c.stepHeader(stepIdx, slug, opt.whatItDoes),
		formatEstimateSuffix(stepEstimate))
	raw, err := json.Marshal(inputs)
	if err != nil {
		return nil, fmt.Errorf("%s: marshal inputs: %w", slug, err)
	}
	runID, err := c.client.RunPipeline(c.ctx, slug, raw)
	if err != nil {
		return nil, fmt.Errorf("%s: dispatch: %w", slug, err)
	}
	c.Logf("  run %s — waiting", runID)

	row, err := c.client.WaitRun(c.ctx, runID, opt.timeout)
	if err != nil {
		return nil, fmt.Errorf("%s: wait run %s: %w", slug, runID, err)
	}
	if row.Status != "completed" {
		msg := row.ErrorMessage
		if msg == "" {
			msg = "no error message"
		}
		return nil, &RunError{Pipeline: slug, RunID: runID, Status: row.Status, Message: msg}
	}
	credSuffix := ""
	if row.CreditsCharged != nil {
		// credits_charged is stored in millicredits; show whole credits.
		credSuffix = fmt.Sprintf(" (%s cr)", formatCreditsMC(*row.CreditsCharged))
	}
	c.Logf("  ✓ %s — completed%s", slug, credSuffix)

	// Persist the step output to state.json so a subsequent --resume
	// run can pick up here. Best-effort; a write failure is logged but
	// doesn't fail the pipeline.
	if err := c.recordStep(stepIdx, c.substepPrefix, slug, runID, row.Output); err != nil {
		c.Logf("  (state.json write failed: %v — resume won't work for this run)", err)
	}

	return &Result{RunID: runID, Output: row.Output, CreditsCharged: row.CreditsCharged}, nil
}

// recordStep appends a successful pipeline dispatch to state.json
// (creating the file if needed). The file is overwritten atomically
// via a tmp-rename so a partial write doesn't corrupt the index.
//
// substep is the fan-out branch prefix (empty for the main chain); it is part
// of the upsert key so parallel branches don't clobber each other's entry.
// The stateMu serialises the load→upsert→write across those parallel branches.
func (c *Context) recordStep(idx int, substep, slug, runID string, output map[string]any) error {
	if c.stateFile == "" {
		return nil
	}
	if c.stateMu != nil {
		c.stateMu.Lock()
		defer c.stateMu.Unlock()
	}

	// Load existing state (if any), then upsert the entry. Idempotent
	// in case the same step somehow runs twice in one process.
	state := &ResumeState{Recipe: c.recipeSlug}
	if existing, err := os.ReadFile(c.stateFile); err == nil {
		_ = json.Unmarshal(existing, state)
		if state.Recipe == "" {
			state.Recipe = c.recipeSlug
		}
	}
	entry := ResumeStep{Idx: idx, Substep: substep, Pipeline: slug, RunID: runID, Output: output}
	found := false
	for i, s := range state.Steps {
		if s.Idx == idx && s.Substep == substep {
			state.Steps[i] = entry
			found = true
			break
		}
	}
	if !found {
		state.Steps = append(state.Steps, entry)
	}

	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(c.stateFile), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	tmp := c.stateFile + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	return os.Rename(tmp, c.stateFile)
}

// LoadResumeState reads state.json from a capture directory. Returns
// (nil, nil) when the file doesn't exist — a fresh run. The caller
// (CLI runner) decides whether to wire the result into a Context via
// WithResume.
func LoadResumeState(captureDir string) (*ResumeState, error) {
	if captureDir == "" {
		return nil, nil
	}
	path := filepath.Join(captureDir, "state.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var state ResumeState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &state, nil
}

// RunOption configures a single RunPipeline call.
type RunOption func(*runOpts)
type runOpts struct {
	timeout    time.Duration
	whatItDoes string
}

// WithStepTimeout overrides the default 15-min wait timeout for one
// step. Useful for long pipelines (e.g. transcription on a 60-min
// episode).
func WithStepTimeout(d time.Duration) RunOption { return func(o *runOpts) { o.timeout = d } }

// WithWhatItDoes labels a conditional or repeated RunPipeline call
// for the runtime logger. By default RunPipeline looks up the slug in
// the manifest's Chain — but a recipe that calls the same pipeline
// twice (e.g. image-generator for persona THEN grid) gets a single
// description for both. WithWhatItDoes overrides the lookup per call
// so dry-run and live logs read correctly.
func WithWhatItDoes(s string) RunOption { return func(o *runOpts) { o.whatItDoes = s } }

// Capture downloads the asset at url into PIPE2_RECIPE_OUT_DIR (when
// the runner was invoked with --capture-to). Step is the chain step
// number — used as the filename's index. Extension is taken from the
// URL's path; auto-detected if absent.
//
// No-op when capture is disabled, so recipes can call Capture
// unconditionally without testing for the env var.
func (c *Context) Capture(step int, url string) error {
	if c.captureDir == "" || url == "" {
		return nil
	}
	ext := strings.TrimPrefix(filepath.Ext(strings.SplitN(url, "?", 2)[0]), ".")
	if ext == "" {
		ext = "bin"
	}
	prefix := fmt.Sprintf("step-%d", step)
	if c.substepPrefix != "" {
		prefix = fmt.Sprintf("%s-%s", c.substepPrefix, prefix)
	}
	out := filepath.Join(c.captureDir, prefix+"."+ext)
	c.Logf("  capturing → %s", out)
	_, err := DownloadFile(c.ctx, ResolveStorageURL(url, c.storageURL), out)
	return err
}

// Result is what RunPipeline returns: the structured output of the
// pipeline keyed by output name. Provides typed shortcut accessors
// for the common "extract a URL" pattern.
type Result struct {
	RunID          string
	Output         map[string]any
	CreditsCharged *int
}

// URL returns Output[key] coerced to string. Panics if absent; that's
// always a recipe bug (the chain step's outputs[] declared the key,
// the worker's contract guarantees it). Recipes catch their own
// errors via the explicit err returned from RunPipeline.
//
// Dry-run exception: when RunID starts with "dry-" and the key is
// missing from the stub Output, return a synthetic placeholder so
// downstream steps in a `--dry-run` walk don't crash on uncommon
// output keys. A real run can never have a dry- RunID.
func (r *Result) URL(key string) string {
	v, ok := r.Output[key]
	if !ok {
		if strings.HasPrefix(r.RunID, "dry-") {
			return "dry://" + r.RunID + "/" + key
		}
		panic(fmt.Sprintf("Result.URL: pipeline did not produce output %q (got: %v)", key, r.Output))
	}
	s, ok := v.(string)
	if !ok {
		panic(fmt.Sprintf("Result.URL: output %q is not a string (got: %T = %v)", key, v, v))
	}
	return s
}

// Float returns Output[key] coerced to float64. Returns the supplied
// default if the key is absent OR not a number — pipeline outputs that
// add a new numeric field over time mustn't break older recipes that
// expect the old shape. JSON unmarshal hands every number back as
// float64, so that's the only coercion we need.
func (r *Result) Float(key string, def float64) float64 {
	v, ok := r.Output[key]
	if !ok {
		return def
	}
	f, ok := v.(float64)
	if !ok {
		return def
	}
	return f
}

// Inputs is the With map authors pass to RunPipeline. Strings,
// numbers, booleans, and nested maps are all valid; the runtime
// json-marshals before dispatch.
type Inputs map[string]any

// RunError describes a non-completed terminal pipeline status. Recipes
// can errors.As into this to react differently to specific failures.
type RunError struct {
	Pipeline string
	RunID    string
	Status   string
	Message  string
}

func (e *RunError) Error() string {
	return fmt.Sprintf("pipeline %s run %s — status=%s: %s", e.Pipeline, e.RunID, e.Status, e.Message)
}

// genqlientClient adapts the existing genqlient-based pipe2 SDK to
// the cookbook's Client interface. The CLI builds one of these from
// MustClient() and passes it to NewContext.
type genqlientClient struct {
	gql graphql.Client
	// uploadFn is wired by the CLI to its existing assets-upload
	// machinery so the cookbook package doesn't depend on
	// internal/cli for a clean import graph.
	uploadFn func(ctx context.Context, localPath string) (string, error)
}

// NewClient wraps a genqlient client + an uploader callback into a
// cookbook.Client. The CLI builds one in `pipe2 recipe run`.
func NewClient(gql graphql.Client, upload func(context.Context, string) (string, error)) Client {
	return &genqlientClient{gql: gql, uploadFn: upload}
}

func (c *genqlientClient) RunPipeline(ctx context.Context, slug string, input json.RawMessage) (string, error) {
	resp, err := pipe2.RunPipeline(ctx, c.gql, slug, input)
	if err != nil {
		return "", err
	}
	if resp.Run_pipeline == nil {
		return "", errors.New("API returned no run id")
	}
	return resp.Run_pipeline.Run_id, nil
}

var terminalStatuses = map[string]bool{
	"completed": true,
	"failed":    true,
	"cancelled": true,
	"timed_out": true,
}

func (c *genqlientClient) WaitRun(ctx context.Context, runID string, timeout time.Duration) (*RunRow, error) {
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		resp, err := pipe2.GetPipelineRun(ctx, c.gql, runID)
		if err != nil {
			return nil, err
		}
		row := resp.Pipeline_runs_by_pk
		if row == nil {
			return nil, fmt.Errorf("run %s not found", runID)
		}
		if terminalStatuses[row.Status] {
			out := map[string]any{}
			if row.Output != nil && len(*row.Output) > 0 {
				if err := json.Unmarshal(*row.Output, &out); err != nil {
					return nil, fmt.Errorf("decode output: %w", err)
				}
			}
			rr := &RunRow{
				ID:             row.Id,
				Status:         row.Status,
				Output:         out,
				CreditsCharged: row.Credits_charged,
			}
			if row.Error_message != nil {
				rr.ErrorMessage = *row.Error_message
			}
			return rr, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-tick.C:
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out after %s", timeout)
		}
	}
}

func (c *genqlientClient) UploadAsset(ctx context.Context, localPath string) (string, error) {
	if c.uploadFn == nil {
		return "", errors.New("cookbook.Client: no upload function configured")
	}
	return c.uploadFn(ctx, localPath)
}

func (c *genqlientClient) EstimatePipelineCost(ctx context.Context, slug string, input json.RawMessage) (*PipelineCostEstimate, error) {
	resp, err := pipe2.EstimatePipelineCost(ctx, c.gql, slug, input)
	if err != nil {
		return nil, err
	}
	if resp.Estimate_pipeline_cost == nil {
		return nil, fmt.Errorf("estimate_pipeline_cost: no payload for %s", slug)
	}
	r := resp.Estimate_pipeline_cost
	return &PipelineCostEstimate{
		PipelineSlug:  r.Pipeline_slug,
		ReservationMC: r.Reservation_mc,
		EstimatedMC:   r.Estimated_mc,
		IsMetered:     r.Is_metered,
	}, nil
}
