// Package clip_factory implements the clip-factory cookbook recipe:
// one long video → N captioned shorter clips. Optionally reformats
// each clip's aspect ratio for short-form platforms.
//
// The chain is:
//
//	transcription → highlights → video-trim × N → [video-reframe?] → captions → watermark
//
// where highlights is skipped when --clips supplies a manual JSON list,
// and video-reframe fires only when --reformat is a non-empty aspect
// ratio. With --reformat unset the source's native aspect is preserved
// (a 16:9 webinar in → 16:9 clips out; a 1:1 source → 1:1 clips).
//
// Source of truth for the article at pipe2.ai/recipes/clip-factory.
package clip_factory

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/pipe2-ai/pipe2-cli/cookbook"
)

func init() {
	cookbook.Register(&Recipe{})
}

type Recipe struct{}

// moment is one editorial pick — either loaded from a --clips JSON file
// (manual override) or parsed from the highlights pipeline output (auto path).
//
// When StartSec and EndSec are both non-zero and StartSec < EndSec, the
// explicit-window path is used: video-trim skips its LLM selection step
// and jumps straight to SliceTranscriptWindow + TrimVideo. Highlights
// always supplies these; manual --clips JSON entries must include them
// too (video-trim no longer has an LLM-pick fallback).
type moment struct {
	Context  string  `json:"context"` // optional: free-text label used in logs and captions metadata only
	StartSec float64 `json:"start_sec"`
	EndSec   float64 `json:"end_sec"`
}

// positionAuto is the sentinel forwarded to the captions pipeline
// when the user wants the burn anchor decided automatically. The
// resolution lives in the captions pipeline itself (subtitles.
// ResolveAutoPosition) so the standalone /pipelines/captions form
// gets the same behaviour — the recipe just passes "auto" through
// and supplies the subject-Y hint below when an upstream reframe ran.
const positionAuto = "auto"

func (r *Recipe) Manifest() cookbook.Manifest {
	// Recipe-specific inputs first; shared watermark inputs spread at
	// the end so the CLI --help ordering reads: source → editorial
	// knobs → quality knobs → branding.
	inputs := []cookbook.Input{
		{Name: "source", Type: cookbook.AssetURL, Required: true, CLIArg: "--input",
			Description: "Source video — a YouTube/social URL, a direct media URL, a local file, or an existing pipe2 asset (URL / /s3 path / id). Remote URLs are resolved on your machine (yt-dlp for streaming/social, plain HTTP for direct links) and uploaded as an asset; the platform never fetches them server-side. yt-dlp + ffmpeg are auto-installed on first use (checksum-verified); set PIPE2_YTDLP_SYSTEM=1 to use ones already on PATH. Use --asset <id> / --no-fetch to skip the fetch for an already-uploaded asset."},
		{Name: "clips", Type: cookbook.String, Default: "", CLIArg: "--clips",
			Description: `Optional path to a JSON file overriding the auto-picker, shaped [{"context": "...", "start_sec": 42.5, "end_sec": 78.0}, ...]. When set, the highlights step is skipped. Leave empty to let the highlights pipeline pick automatically.`},
		{Name: "highlights_count", Type: cookbook.Int, Default: int64(5), CLIArg: "--highlights-count",
			Description: "How many moments to pick when highlights runs (auto mode). Ignored if --clips is set."},
		{Name: "highlights_style", Type: cookbook.String, Default: "", CLIArg: "--highlights-style",
			Description: `Natural-language steer for the highlights picker — e.g. "the funniest moments", "the strongest arguments". Empty uses the picker's default.`},
		// Default: serif-editorial — restrained Noto Serif on a
		// translucent dark card. Designed for technical / podcast
		// content where viewers should READ the words, not chase
		// a flickering highlight. Switch to karaoke-gradient or
		// tiktok-bold-yellow for viral / dance / hook content.
		{Name: "preset", Type: cookbook.Enum, Default: "serif-editorial", CLIArg: "--preset",
			Values:      []string{"tiktok-bold-yellow", "minimal-white", "subtle-drop", "karaoke-gradient", "big-serif", "serif-editorial"},
			Description: "Caption styling preset for every clip."},
		{Name: "position", Type: cookbook.Enum, Default: positionAuto, CLIArg: "--position",
			Values:      []string{"auto", "top", "middle", "bottom"},
			Description: `Vertical anchor for the burned captions. "auto" (default) lets the captions pipeline place the text on the opposite half of the frame from the subject — the reframe step's subject-Y hint feeds the decision when --reformat is set; otherwise auto falls through to "bottom". Set explicitly to override.`},
		// Empty (default) means "preserve source aspect, skip the
		// reframe step entirely". The chain step declares
		// optional_when_empty: "reformat" so the recipe-page cost
		// widget and the LLM-friendly Manifest() shape both know
		// to mark step 4 as default-skipped.
		{Name: "reformat", Type: cookbook.Enum, Default: "", CLIArg: "--reformat",
			Values:      []string{"", "9:16", "1:1", "4:5", "16:9"},
			Description: "Optional output aspect ratio. Leave empty to preserve the source's native aspect (default — fastest, cheapest). Set to 9:16 for TikTok/Reels/Shorts, 1:1 or 4:5 for Instagram, 16:9 for horizontal YouTube cards from a vertical source."},
		{Name: "language", Type: cookbook.String, Default: "en", CLIArg: "--lang",
			Description: "ISO 639-1 transcription language, or 'auto'."},
		// Routes through transcription so the LLM-driven trim step
		// AND the burned captions both read the corrected SRT.
		{Name: "corrections", Type: cookbook.String, Default: "", CLIArg: "--corrections",
			Description: "Comma-separated word-boundary substitutions applied to the transcript, in the form \"from=to,from=to\". Use it when the recognizer mis-hears the same word the same way every time (a name, an acronym, a domain term). To preserve a phrase that contains a substring you also want to rewrite, declare the longer phrase first as a no-op (\"phrase=phrase,word=replacement\") — longer matches win, so the phrase is shielded before the bare-word rule fires. Case-sensitive."},
		{Name: "parallelism", Type: cookbook.Int, Default: int64(4), CLIArg: "--parallel",
			Description: "Max number of clips to process in parallel."},
	}
	inputs = append(inputs, cookbook.WatermarkInputs()...)

	return cookbook.Manifest{
		Slug:           "clip-factory",
		Title:          "Long video → multiple captioned clips, in one command",
		Description:    "Slice any long video into N captioned shorter clips. Transcribe once, auto-pick the moments with AI, trim + caption each in parallel. Optionally reformat to vertical (9:16) for TikTok / Reels / Shorts, square (1:1) for Instagram, or portrait (4:5) — caption anchor auto-adjusts.",
		IntroVoiceover: "Got a long video and want a dozen shorter ones out of it? Transcribe the whole thing once, let AI pick the best moments, then loop trim and caption per clip. Pass --reformat to crop to vertical for TikTok or Reels; leave it off and the source aspect is preserved for YouTube cards or Instagram feeds.",
		Category:       "tutorial",
		Tags:           []string{"cli", "claude-code", "captions", "transcription", "highlights", "video-trim", "video-reframe", "shorts", "vertical-video", "tiktok", "reels", "instagram", "youtube-shorts", "agent-loop"},
		Audience:       []string{"creator", "podcaster", "agency editor", "AI agent"},
		SourceVideo: &cookbook.VideoSource{
			URL:   "https://www.youtube.com/watch?v=4uzGDAoNOZc",
			Title: "OpenClaw Creator — Why 80% of Apps Will Disappear",
			Note:  "Demo previews are five clips cut from this interview. Drop in any long-form video to make your own.",
		},
		PublishedAt: "2026-05-20",
		UpdatedAt:   "2026-05-20",
		Inputs:      inputs,
		Chain: []cookbook.ChainStep{
			{Pipeline: "transcription", ArtifactKind: cookbook.Text,
				WhatItDoes: "ElevenLabs Scribe transcribes the full source once — cached on the source hash, so re-runs and every clip after the first are free. The trim reads these words to find each moment."},
			{Pipeline: "highlights", ArtifactKind: cookbook.JSON, OptionalWhenEmpty: "clips",
				WhatItDoes: "Reads the transcript and picks N editorial moments — the auto-pick path. Skipped when --clips supplies a manual JSON list."},
			{Pipeline: "video-trim", ArtifactKind: cookbook.Video,
				WhatItDoes: "Per clip: deterministic SRT-slice + ffmpeg-cut to the window highlights picked, snapped to sentence boundaries. Returns the windowed transcript rebased to the clip for the captions step."},
			{Pipeline: "video-reframe", ArtifactKind: cookbook.Video, OptionalWhenEmpty: "reformat",
				WhatItDoes: "Per clip (only when --reformat is set): reframes to the requested aspect ratio with the lock-and-cut camera director — it frames the active speaker in every shot (from the windowed transcript + CV faces) and cuts cleanly at shot boundaries, never drifting or panning. Skipped by default — the source's native aspect is preserved."},
			{Pipeline: "captions", ArtifactKind: cookbook.Video,
				WhatItDoes: "Per clip: burns the windowed transcript onto the clip in your chosen preset, at the anchor --position picks."},
			cookbook.WatermarkChainStep(),
		},
		ExampleCommand: "pipe2 recipe run clip-factory --input https://www.youtube.com/watch?v=4uzGDAoNOZc --reformat 9:16",
		AgentPrompt: strings.Join([]string{
			"Run the pipe2 clip-factory recipe — one long video → N captioned, watermarked clips. Picks moments automatically.",
			"",
			"Dispatch: pipe2 recipe run clip-factory --input <video-url-or-local-path> --reformat 9:16",
			"",
			"Tune the picker with --highlights-count N and --highlights-style \"the funniest moments\". Power-user override: --clips path/to/clips.json (JSON array of {\"context\",\"start_sec\",\"end_sec\"}).",
			"",
			"Returns a JSON array of clip URLs.",
		}, "\n"),
	}
}

func (r *Recipe) Run(ctx *cookbook.Context) error {
	source, err := ctx.ResolveSourceURL("source")
	if err != nil {
		return err
	}
	clipsPath := ctx.Inputs.String("clips")

	// Corrections parsed up-front so a malformed --corrections flag
	// fails the recipe BEFORE the user pays for the transcription.
	corrections, err := cookbook.ParseCorrections(ctx.Inputs.String("corrections"))
	if err != nil {
		return fmt.Errorf("--corrections: %w", err)
	}

	reformat := ctx.Inputs.String("reformat")
	position := ctx.Inputs.String("position")
	if position == "" {
		position = positionAuto
	}

	// Step 1: Transcription (always runs).
	episode, err := ctx.RunPipeline("transcription", cookbook.Inputs{
		"source_asset_id": source,
		"language_code":   ctx.Inputs.String("language"),
		"diarize":         true,
		"corrections":     corrections,
	}, cookbook.WithStepTimeout(transcribeTimeout))
	if err != nil {
		return err
	}
	episodeSRT := episode.URL("srt_asset_url")
	_ = ctx.Capture(1, episodeSRT)

	// Step 2: Moments — either from a --clips file (manual override) or
	// from the highlights pipeline (auto path).
	var moments []moment
	if clipsPath != "" {
		// Manual path: load from file.
		moments, err = loadMoments(clipsPath)
		if err != nil {
			return err
		}
		if len(moments) == 0 {
			return fmt.Errorf("clips file %s is empty", clipsPath)
		}
		ctx.Logf("manual clips loaded — %d clip%s queued", len(moments), pluralS(len(moments)))
	} else {
		// Auto path: dispatch highlights pipeline.
		h, err := ctx.RunPipeline("highlights", cookbook.Inputs{
			"transcript_asset_id": episodeSRT,
			"count":               ctx.Inputs.Int("highlights_count"),
			"style":               ctx.Inputs.String("highlights_style"),
			"language":            ctx.Inputs.String("language"),
		})
		if err != nil {
			return fmt.Errorf("highlights: %w", err)
		}
		_ = ctx.Capture(2, h.URL("highlights_url"))

		// Parse the inlined highlights list from the pipeline output.
		// The JSON output is []any of map[string]any from JSON unmarshal.
		rawHighlights, _ := h.Output["highlights"]
		if rawHighlights != nil {
			if arr, ok := rawHighlights.([]any); ok {
				for _, item := range arr {
					if m, ok := item.(map[string]any); ok {
						ctx_ := fmt.Sprintf("%v", m["context"])
						startSec := 0.0
						if v, ok := m["start_sec"].(float64); ok {
							startSec = v
						}
						endSec := 0.0
						if v, ok := m["end_sec"].(float64); ok {
							endSec = v
						}
						moments = append(moments, moment{
							Context:  ctx_,
							StartSec: startSec,
							EndSec:   endSec,
						})
					}
				}
			}
		}
		if len(moments) == 0 {
			return fmt.Errorf("highlights pipeline returned no moments")
		}
		ctx.Logf("highlights picked — %d clip%s queued", len(moments), pluralS(len(moments)))
	}

	if reformat != "" {
		ctx.Logf("will reformat each clip to %s", reformat)
	}

	// Resolve the watermark asset ONCE up-front — every parallel clip
	// reuses the same uploaded URL via closure.
	watermarkURL, err := cookbook.ResolveWatermark(ctx)
	if err != nil {
		return err
	}
	watermarkScale := ctx.Inputs.Int("watermark_scale_pct")

	limit := int(ctx.Inputs.Int("parallelism"))
	if limit < 1 {
		limit = 1
	}
	clipURLs := make([]string, len(moments))
	g, gctx := errgroup.WithContext(ctx.Ctx())
	g.SetLimit(limit)

	preset := ctx.Inputs.String("preset")

	for i, m := range moments {
		i, m := i, m
		g.Go(func() error {
			sub := ctx.Substep(i + 1).WithContext(gctx)
			sub.Logf("clip %d — %s", i+1, m.Context)

			// Trim — explicit-window only. Highlights always emits
			// start_sec/end_sec; manual --clips entries must include the
			// same so video-trim can deterministically slice + ffmpeg-cut.
			if !(m.EndSec > m.StartSec) {
				return fmt.Errorf("clip %d: highlights or --clips entry missing start_sec/end_sec (got %.2f..%.2f)", i+1, m.StartSec, m.EndSec)
			}
			trim, err := sub.RunPipeline("video-trim", cookbook.Inputs{
				"video_url":      source,
				"transcript_url": episodeSRT,
				"start_sec":      m.StartSec,
				"end_sec":        m.EndSec,
			})
			if err != nil {
				return fmt.Errorf("clip %d trim: %w", i+1, err)
			}
			trimURL := trim.URL("video_url")
			clipSRT := trim.URL("transcript_url")
			_ = sub.Capture(3, trimURL)

			captionInput := trimURL
			var subjectYPct float64
			if reformat != "" {
				reframe, err := sub.RunPipeline("video-reframe", cookbook.Inputs{
					"video_url":           trimURL,
					"target_aspect_ratio": reformat,
					// transcript_url: the windowed SRT video-trim just
					// produced. Its diarized speaker turns drive the V4
					// camera director's audio-first focal resolution, so
					// two-shot panels follow whoever the transcript
					// attributes each turn to. Lock-and-cut is deterministic
					// — there are no follow / motion-smoothing knobs.
					"transcript_url": clipSRT,
				})
				if err != nil {
					return fmt.Errorf("clip %d reframe: %w", i+1, err)
				}
				captionInput = reframe.URL("video_url")
				subjectYPct = reframe.Float("subject_y_pct", 0)
				_ = sub.Capture(4, captionInput)
			}

			caps, err := sub.RunPipeline("captions", cookbook.Inputs{
				"source_asset_id":     captionInput,
				"transcript_asset_id": clipSRT,
				"preset_name":         preset,
				"position":            position,
				"subject_y_pct":       subjectYPct,
			})
			if err != nil {
				return fmt.Errorf("clip %d captions: %w", i+1, err)
			}
			final := caps.URL("video_url")
			_ = sub.Capture(5, final)

			branded, err := cookbook.ApplyWatermark(sub, final, watermarkURL, watermarkScale)
			if err != nil {
				return fmt.Errorf("clip %d watermark: %w", i+1, err)
			}
			if branded != final {
				final = branded
				_ = sub.Capture(6, final)
			}

			clipURLs[i] = final
			sub.Logf("clip %d ready: %s", i+1, final)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	out, _ := json.Marshal(clipURLs)
	ctx.SetOutput(string(out))
	ctx.Logf("all %d clip%s ready", len(clipURLs), pluralS(len(clipURLs)))
	return nil
}

// loadMoments parses the user-supplied clips.json. Tolerates either an
// array-of-objects or a single object so a one-clip dry-run works
// without wrapping.
func loadMoments(path string) ([]moment, error) {
	if path == "" {
		return nil, fmt.Errorf("--clips path is empty")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var arr []moment
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr, nil
	}
	var single moment
	if err := json.Unmarshal(raw, &single); err == nil && single.Context != "" {
		return []moment{single}, nil
	}
	return nil, fmt.Errorf("%s isn't a valid clips JSON: expected array of {context, start_sec, end_sec}", path)
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// transcribeTimeout caps the wait on the full source transcription.
// 30 minutes covers ~3-hour episodes at typical worker throughput;
// the cache hit path returns in seconds.
const transcribeTimeout = 30 * time.Minute
