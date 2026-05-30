// Package cookbook is the typed runtime for Pipe2 recipes.
//
// A recipe is a Go program that orchestrates one or more Pipe2
// pipeline calls. Each lives in its own package under
// pipe2-cli/cookbook/<slug>/ and registers itself with the global
// registry via init() → Register().
//
// The recipe author writes:
//   - Manifest()   metadata + chain shape, used by the article
//     walkthrough renderer and `pipe2 recipe info`
//   - Run(ctx)     the actual orchestration: typed inputs, pipeline
//     invocations, output capture, error handling
//
// The CLI binary embeds every registered recipe at compile time.
// This eliminates the recipe.sh + recipe.json dual source of truth:
// the Go function is the runnable, and the manifest.json shipped to
// the website is *generated* from Manifest() rather than hand-edited.
//
// Recipes will move to their own public github.com/pipe2-ai/pipe2-cli
// repository alongside this package once the cross-repo migration
// lands. Until then they live in this monorepo.
package cookbook

// SchemaVersion is the highest manifest schema this binary speaks.
// Generated manifest.json files include this; the web loader rejects
// versions it doesn't recognize.
const SchemaVersion = 1

// The Pipe2 brand watermark recipes apply by default lives as an
// embedded asset in watermark.go (MaterializeDefaultWatermark) — that
// removes the need for a public URL constant and makes the default
// work in any deploy context (dev docker, CI, hosted CLI).

// InputType drives both CLI flag coercion and article-form rendering.
type InputType string

const (
	String   InputType = "string"
	Int      InputType = "int"
	Bool     InputType = "bool"
	Enum     InputType = "enum"
	AssetURL InputType = "asset_url" // accepts a URL or a local path; auto-uploads local
)

// ArtifactKind tells the article walkthrough how to render a step's
// output. The runtime ignores it.
type ArtifactKind string

const (
	Video    ArtifactKind = "video"
	Image    ArtifactKind = "image"
	Text     ArtifactKind = "text"
	Audio    ArtifactKind = "audio"
	JSON     ArtifactKind = "json"
	NoneKind ArtifactKind = "none"
)

// Manifest is the metadata projection of a recipe. It mirrors the
// Zod schema in packages/web/src/content.config.ts — both must move
// together when fields change. JSON tags use snake_case to match
// what the loader expects.
type Manifest struct {
	SchemaVersion int `json:"schema_version"`

	Slug           string `json:"slug"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	IntroVoiceover string `json:"intro_voiceover,omitempty"`

	Category string   `json:"category,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Audience []string `json:"audience,omitempty"`
	SeeAlso  []string `json:"see_also,omitempty"`

	// SourceVideo credits the long-form clip this recipe's published
	// sample was cut from. Rendered on the first walkthrough step —
	// the one that ingests it — as a provenance link.
	SourceVideo *VideoSource `json:"source_video,omitempty"`

	Inputs []Input     `json:"-"` // serialized as a map below for JSON
	Chain  []ChainStep `json:"chain"`

	PublishedAt string `json:"published_at,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`

	// Optional overrides for the runnable example surfaces on the
	// /recipes article. When empty the website auto-generates them
	// from input shape (URL-in recipes get a sample-URL command,
	// text-in recipes get the defaults-only form). Declare these
	// when the recipe needs a non-trivial canonical example — e.g.
	// multiple required flags the auto-template can't fill in.
	ExampleCommand string `json:"example_command,omitempty"`
	AgentPrompt    string `json:"agent_prompt,omitempty"`
}

// VideoSource is a provenance link to an external source clip — the
// long-form video a recipe's published sample was produced from.
type VideoSource struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	// Note is an optional caption — e.g. flagging that the demo
	// previews were built from an excerpt rather than the whole clip.
	Note string `json:"note,omitempty"`
}

// Input declares one user-facing parameter on a recipe. The CLI
// builds dynamic --flags from these; the article renders form
// fields. Inputs are addressed by Name; CLIArg is the public flag
// authors expose (defaults to --<name> if empty).
type Input struct {
	Name        string    `json:"-"` // map key in the JSON projection
	Type        InputType `json:"type"`
	Required    bool      `json:"required,omitempty"`
	Default     any       `json:"default,omitempty"`
	Values      []string  `json:"values,omitempty"`
	Min         *float64  `json:"min,omitempty"`
	Max         *float64  `json:"max,omitempty"`
	CLIArg      string    `json:"cli_arg,omitempty"`
	Description string    `json:"description,omitempty"`
}

// ChainStep is the article-walkthrough projection of one pipeline
// invocation. The runtime doesn't iterate this; it's metadata for
// the renderer. Authors update it when the Run() function adds or
// removes a step.
type ChainStep struct {
	Pipeline     string       `json:"pipeline"`
	WhatItDoes   string       `json:"what_it_does,omitempty"`
	ArtifactKind ArtifactKind `json:"artifact_kind,omitempty"`

	// Pricing-affecting input pins (model, duration, resolution).
	// Used by the recipe article to compute the displayed estimate
	// from the pinned tier instead of the cheapest-tier floor.
	// Strings can reference recipe inputs via `${inputs.<name>}`.
	// Keep in sync with the corresponding ctx.RunPipeline(...) call
	// in recipe.go — drift means the displayed cost is wrong.
	With map[string]any `json:"with,omitempty"`

	// Names the recipe input whose emptiness skips this step in a
	// defaults-only run (e.g. "watermark_url"). Article excludes
	// this step from the total and annotates the row with the flag.
	OptionalWhenEmpty string `json:"optional_when_empty,omitempty"`
}

// CLIFlag returns the public flag name (--source, --preset, …) for
// an input, defaulting to "--" + the snake_case name when the
// author hasn't set CLIArg explicitly.
func (i Input) CLIFlag() string {
	if i.CLIArg != "" {
		return i.CLIArg
	}
	return "--" + i.Name
}
