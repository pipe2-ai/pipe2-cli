package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pipe2-ai/pipe2-cli/cookbook"
	_ "github.com/pipe2-ai/pipe2-cli/cookbook/all" // populate registry
)

// newRecipeCmd builds the `pipe2 recipe ...` subcommand tree.
//
// Recipes are Go programs registered at init() time in the cookbook
// package. The CLI binary embeds them — no fetch, no bash, no cache.
// Editing a recipe requires a CLI release; trade-off accepted in the
// design doc (docs/plans/2026-05-02-cookbook-learn-feature-design.md).
func newRecipeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "recipe",
		Aliases: []string{"recipes"},
		Short:   "Run cookbook recipes",
		Long: `Recipes are typed Go programs that orchestrate one or more Pipe2
pipelines. They ship with the CLI binary.

Examples:
  pipe2 recipe list
  pipe2 recipe info clip-factory
  pipe2 recipe run clip-factory --input https://example.com/talk.mp4 --reformat 9:16
  pipe2 recipe run clip-factory --input ./my-clip.mp4 --highlights-count 3 --preset karaoke-gradient`,
	}
	c.AddCommand(
		newRecipeListCmd(),
		newRecipeInfoCmd(),
		newRecipeRunCmd(),
		newRecipeDownloadCmd(),
	)
	return c
}

func newRecipeListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List recipes compiled into this binary",
		RunE: func(_ *cobra.Command, _ []string) error {
			recipes := cookbook.All()
			if Globals.JSON {
				out := make([]cookbook.Manifest, len(recipes))
				for i, r := range recipes {
					m := r.Manifest()
					m.FillSchemaVersion()
					out[i] = m
				}
				return Out(out)
			}
			fmt.Fprintf(os.Stderr, "%d recipe%s\n", len(recipes), plural(len(recipes)))
			for _, r := range recipes {
				m := r.Manifest()
				fmt.Fprintf(os.Stdout, "  %-30s  %d steps  %s\n", m.Slug, len(m.Chain), m.Description)
			}
			return nil
		},
	}
}

func newRecipeInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <slug>",
		Short: "Pretty-print a recipe's manifest",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			r, ok := cookbook.Lookup(args[0])
			if !ok {
				return &ExitError{Code: ExitNotFound, Err: fmt.Errorf("no recipe named %q (try `pipe2 recipe list`)", args[0])}
			}
			m := r.Manifest()
			m.FillSchemaVersion()
			if Globals.JSON {
				return Out(m)
			}
			return printRecipeInfo(m, os.Stdout)
		},
	}
}

func newRecipeRunCmd() *cobra.Command {
	var captureDir string
	var resume bool
	var dryRun bool
	var estimate bool
	var noFetch bool
	var assetRef string
	c := &cobra.Command{
		Use:                "run <slug> [--<input> <value> ...]",
		Short:              "Execute a recipe end-to-end",
		Args:               cobra.ExactArgs(1),
		FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
		RunE: func(cmd *cobra.Command, args []string) error {
			r, ok := cookbook.Lookup(args[0])
			if !ok {
				return &ExitError{Code: ExitNotFound, Err: fmt.Errorf("no recipe named %q (try `pipe2 recipe list`)", args[0])}
			}
			m := r.Manifest()

			supplied, err := parseDynamicInputs(m, os.Args[1:])
			if err != nil {
				return &ExitError{Code: ExitUsage, Err: err}
			}
			// --asset <id> is sugar for "the source is this already-uploaded
			// asset": set the recipe's source input to it and skip the
			// client-side fetch/upload entirely. It maps to the conventional
			// `source` input (clip-factory et al.); recipes with differently
			// named asset inputs use `--<name> <ref> --no-fetch` instead.
			if assetRef != "" {
				if !recipeHasInput(m, "source") {
					return &ExitError{Code: ExitUsage, Err: fmt.Errorf(
						"--asset sets the `source` input, which %q does not have; pass the asset reference to the recipe's own asset flag together with --no-fetch instead", m.Slug)}
				}
				supplied["source"] = assetRef
				noFetch = true
			}
			inputs, err := cookbook.ResolveInputs(m, supplied)
			if err != nil {
				return &ExitError{Code: ExitUsage, Err: err}
			}

			// Dry-run doesn't dispatch, so it doesn't need a live API
			// client. Skipping MustClient() means `--dry-run` works
			// without auth — a creator can sanity-check the chain
			// before logging in. --estimate (with or without --dry-run)
			// DOES need auth: the cost-lookup action is user-scoped so
			// asset-duration resolution can check ownership.
			var client cookbook.Client
			if !dryRun || estimate {
				gql, err := MustClient()
				if err != nil {
					return err
				}
				client = cookbook.NewClient(gql, func(ctx context.Context, path string) (string, error) {
					return uploadLocalForRecipe(ctx, gql, path)
				})
			}

			cfg, _ := LoadConfig()
			ctxOpts := []cookbook.ContextOption{
				cookbook.WithStdout(os.Stdout),
				cookbook.WithStderr(os.Stderr),
				cookbook.WithRecipeSlug(m.Slug),
				cookbook.WithChain(m.Chain),
				cookbook.WithStorageURL(cfg.EffectiveStorageURL()),
			}
			if dryRun {
				ctxOpts = append(ctxOpts, cookbook.WithDryRun(true))
			}
			if estimate {
				ctxOpts = append(ctxOpts, cookbook.WithEstimate(true))
			}
			if noFetch {
				ctxOpts = append(ctxOpts, cookbook.WithNoFetch(true))
			}
			if captureDir != "" {
				ctxOpts = append(ctxOpts, cookbook.WithCaptureDir(captureDir))
			}

			// Resume: load state.json from captureDir when --resume is
			// set. A slug-mismatch is a user error (pointing at the
			// wrong recipe's state); we fail fast rather than ignore.
			if resume {
				if captureDir == "" {
					return &ExitError{Code: ExitUsage, Err: fmt.Errorf("--resume requires --capture-to")}
				}
				state, err := cookbook.LoadResumeState(captureDir)
				if err != nil {
					return &ExitError{Code: ExitGeneric, Err: fmt.Errorf("load resume state: %w", err)}
				}
				if state == nil {
					// Plain Fprintf (not Status) — Status is suppressed when
					// stdout isn't a TTY (JSON mode auto-on for agents), and
					// resume diagnostics MUST be visible to humans.
					fmt.Fprintf(os.Stderr, "--resume requested but no state.json in %s — running from scratch\n", captureDir)
				} else {
					if state.Recipe != "" && state.Recipe != m.Slug {
						return &ExitError{Code: ExitUsage, Err: fmt.Errorf("state.json is from recipe %q, not %q — point --capture-to elsewhere", state.Recipe, m.Slug)}
					}
					fmt.Fprintf(os.Stderr, "--resume: %d step%s already completed in %s\n", len(state.Steps), plural(len(state.Steps)), captureDir)
					ctxOpts = append(ctxOpts, cookbook.WithResume(state))
				}
			}

			rctx := cookbook.NewContext(cmd.Context(), client, inputs, ctxOpts...)

			mode := "running"
			if dryRun {
				mode = "dry-running"
			}
			Status("%s recipe %s — %d step%s", mode, m.Slug, len(m.Chain), plural(len(m.Chain)))
			if err := r.Run(rctx); err != nil {
				return &ExitError{Code: ExitGeneric, Err: err}
			}
			if estimate {
				reservation, actual, unavailable := rctx.EstimateTotals()
				if reservation > 0 || actual > 0 {
					verb := "would cost"
					if !dryRun {
						verb = "charged"
					}
					line := fmt.Sprintf("estimate · %s %s cr", verb, fmtCreditsMC(actual))
					if reservation > actual {
						line += fmt.Sprintf(" (held up to %s cr)", fmtCreditsMC(reservation))
					}
					if unavailable > 0 {
						line += fmt.Sprintf("; %d step%s unavailable", unavailable, plural(unavailable))
					}
					fmt.Fprintln(os.Stderr, line)
				}
			}
			if final := rctx.FinalOutput(); final != "" {
				fmt.Fprintln(os.Stdout, final)
			}
			return nil
		},
	}
	c.Flags().StringVar(&captureDir, "capture-to", "",
		"directory where Capture writes per-step artifacts (used by the asset-production pipeline)")
	c.Flags().BoolVar(&resume, "resume", false,
		"reuse step outputs recorded in <capture-to>/state.json from a prior run; only steps not yet recorded are dispatched")
	c.Flags().BoolVar(&dryRun, "dry-run", false,
		"resolve inputs and log the chain that would run, but skip dispatch (no credits charged, no auth required)")
	c.Flags().BoolVar(&estimate, "estimate", false,
		"fetch credit cost for each step via the API and print a running total (composes with --dry-run for a no-spend cost preview; requires auth)")
	c.Flags().BoolVar(&noFetch, "no-fetch", false,
		"treat every source input as an already-uploaded asset reference (URL / id / /s3 path) and pass it through verbatim — skip the client-side download + upload of remote URLs")
	c.Flags().StringVar(&assetRef, "asset", "",
		"shortcut for an already-uploaded source asset: equivalent to setting the recipe's `source` input to <id-or-url> with --no-fetch")
	return c
}

// recipeHasInput reports whether the recipe declares an input with the
// given Name. Used to validate --asset, which targets the conventional
// `source` input.
func recipeHasInput(m cookbook.Manifest, name string) bool {
	for _, in := range m.Inputs {
		if in.Name == name {
			return true
		}
	}
	return false
}

// fmtCreditsMC renders millicredits as a compact credit string for the
// estimate summary footer — mirrors the helper inside cookbook so the
// runner footer matches the per-step suffix exactly.
func fmtCreditsMC(mc int) string {
	c := float64(mc) / 1000.0
	if c == float64(int(c)) {
		return fmt.Sprintf("%d", int(c))
	}
	return fmt.Sprintf("%.1f", c)
}

func printRecipeInfo(m cookbook.Manifest, w io.Writer) error {
	fmt.Fprintf(w, "%s · v%d\n%s\n\n", m.Slug, m.SchemaVersion, m.Title)
	if m.Description != "" {
		fmt.Fprintf(w, "%s\n\n", m.Description)
	}
	if len(m.Inputs) > 0 {
		// Stable order for human display.
		ins := make([]cookbook.Input, len(m.Inputs))
		copy(ins, m.Inputs)
		sort.Slice(ins, func(i, j int) bool { return ins[i].Name < ins[j].Name })
		fmt.Fprintln(w, "Inputs:")
		for _, in := range ins {
			req := ""
			if in.Required {
				req = " (required)"
			}
			def := ""
			if in.Default != nil {
				def = fmt.Sprintf(" — default: %v", in.Default)
			}
			fmt.Fprintf(w, "  %s  %s%s%s\n", in.CLIFlag(), in.Type, req, def)
			if in.Description != "" {
				fmt.Fprintf(w, "      %s\n", in.Description)
			}
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "Chain:")
	for i, s := range m.Chain {
		fmt.Fprintf(w, "  %d. %s — %s\n", i+1, s.Pipeline, s.WhatItDoes)
	}
	if m.SourceVideo != nil {
		fmt.Fprintf(w, "\nSample source: %s — %s\n", m.SourceVideo.Title, m.SourceVideo.URL)
	}
	return nil
}

// parseDynamicInputs walks the raw command line for --<key>[=<val>] /
// --<key> <val> pairs that match recipe input declarations.
func parseDynamicInputs(m cookbook.Manifest, argv []string) (map[string]string, error) {
	known := map[string]bool{}
	for _, in := range m.Inputs {
		known[strings.TrimLeft(in.CLIFlag(), "-")] = true
		known[in.Name] = true
	}
	out := map[string]string{}
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if !strings.HasPrefix(a, "--") {
			continue
		}
		body := strings.TrimLeft(a, "-")
		key, val, hasEq := strings.Cut(body, "=")
		if !known[key] {
			continue
		}
		if hasEq {
			out[key] = val
			continue
		}
		if i+1 >= len(argv) {
			return nil, fmt.Errorf("--%s expects a value", key)
		}
		out[key] = argv[i+1]
		i++
	}
	return out, nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
