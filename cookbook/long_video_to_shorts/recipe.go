// Package long_video_to_shorts turns one long video into captioned social clips.
package long_video_to_shorts

import (
	"encoding/json"
	"fmt"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/pipe2-ai/pipe2-cli/cookbook"
)

func init() { cookbook.Register(&Recipe{}) }

type Recipe struct{}

const defaultInstructions = "Find the five strongest self-contained moments. Prefer clear ideas, surprising claims, useful explanations, or complete stories. Each clip should make sense without the rest of the video."

func (*Recipe) Manifest() cookbook.Manifest {
	minOne, maxTen, maxSeconds := 1.0, 10.0, 120.0
	return cookbook.Manifest{
		Slug:           "long-video-to-shorts",
		Title:          "Long video → captioned Shorts",
		Description:    "Give AI a long video and an editorial brief, then get a pack of selected, reframed, and captioned social clips.",
		IntroVoiceover: "The recipe builds a timestamped source transcript, then Video Trim reviews the complete recording and chooses clean moments. Each result is reframed, transcribed to its final timing, and captioned as its own shareable clip.",
		Category:       "tutorial",
		Tags:           []string{"highlights", "video-trim", "video-reframe", "captions", "shorts", "vertical-video", "podcast"},
		Audience:       []string{"creator", "podcaster", "agency editor", "social team"},
		SourceVideo: &cookbook.VideoSource{
			URL:   "https://www.youtube.com/watch?v=4uzGDAoNOZc",
			Title: "OpenClaw Creator: Why 80% Of Apps Will Disappear",
			Note:  "The sample gallery was produced by this recipe from the credited OpenClaw interview.",
		},
		PublishedAt: "2026-09-20",
		UpdatedAt:   "2026-09-21",
		Inputs: []cookbook.Input{
			{Name: "source", Type: cookbook.AssetURL, Required: true, CLIArg: "--input", Description: "Source video: a YouTube or social URL, direct media URL, local file, or existing Pipe2 asset."},
			{Name: "instructions", Type: cookbook.String, Default: defaultInstructions, CLIArg: "--instructions", Description: "Editorial brief describing which moments to select and what makes a good clip."},
			{Name: "max_clips", Type: cookbook.Int, Default: int64(5), Min: &minOne, Max: &maxTen, CLIArg: "--max-clips", Description: "Maximum number of clips Video Trim may return."},
			{Name: "max_clip_duration_sec", Type: cookbook.Int, Default: int64(45), Min: &minOne, Max: &maxSeconds, CLIArg: "--max-seconds", Description: "Maximum duration of each selected clip, in seconds."},
			{Name: "aspect_ratio", Type: cookbook.Enum, Default: "9:16", CLIArg: "--aspect", Values: []string{"9:16", "1:1", "4:5", "16:9"}, Description: "Output aspect ratio for every clip."},
			{Name: "preset", Type: cookbook.Enum, Default: "serif-editorial", CLIArg: "--preset", Values: []string{"tiktok-bold-yellow", "minimal-white", "subtle-drop", "karaoke-gradient", "big-serif", "serif-editorial"}, Description: "Caption style used for every clip."},
			{Name: "position", Type: cookbook.Enum, Default: "auto", CLIArg: "--position", Values: []string{"auto", "top", "middle", "bottom"}, Description: "Caption position. Auto keeps text away from the main subject when possible."},
			{Name: "language", Type: cookbook.String, Default: "auto", CLIArg: "--lang", Description: "ISO 639-1 transcription language, or auto."},
			{Name: "corrections", Type: cookbook.String, Default: "", CLIArg: "--corrections", Description: "Optional comma-separated word corrections in the form from=to,from=to."},
			{Name: "parallelism", Type: cookbook.Int, Default: int64(4), Min: &minOne, Max: &maxTen, CLIArg: "--parallel", Description: "Maximum number of selected clips processed in parallel."},
		},
		Chain: []cookbook.ChainStep{
			{Pipeline: "transcription", ArtifactKind: cookbook.Text, WhatItDoes: "Creates the timestamped speech map Video Trim uses to keep every selected idea complete."},
			{Pipeline: "video-trim", ArtifactKind: cookbook.Video, WhatItDoes: "Reviews the complete source, selects the strongest moments from the editorial brief, and returns separate clips."},
			{Pipeline: "video-reframe", ArtifactKind: cookbook.Video, WhatItDoes: "Reframes every selected clip to the chosen social aspect ratio while keeping speakers and important content visible."},
			{Pipeline: "transcription", ArtifactKind: cookbook.Text, WhatItDoes: "Transcribes each reframed clip so its captions match the final edit and timing."},
			{Pipeline: "captions", ArtifactKind: cookbook.Video, WhatItDoes: "Burns the matching transcript into every clip with the selected style and position."},
		},
		ExampleCommand: "pipe2 recipe run long-video-to-shorts --input https://www.youtube.com/watch?v=4uzGDAoNOZc",
		AgentPrompt:    "Run the long-video-to-shorts recipe with the pipe2 CLI. Pass the source with --input and describe the moments to select with --instructions. Use --max-clips, --max-seconds, --aspect, and --preset when the user specifies them. Report the returned JSON array of captioned clip URLs.",
	}
}

func (*Recipe) Run(ctx *cookbook.Context) error {
	source, err := ctx.ResolveSourceURL("source")
	if err != nil {
		return err
	}
	instructions := strings.TrimSpace(ctx.Inputs.String("instructions"))
	if len(instructions) < 3 || len(instructions) > 4000 {
		return fmt.Errorf("instructions must contain 3–4000 characters")
	}
	corrections, err := cookbook.ParseCorrections(ctx.Inputs.String("corrections"))
	if err != nil {
		return fmt.Errorf("--corrections: %w", err)
	}
	sourceTranscript, err := ctx.RunPipeline("transcription", cookbook.Inputs{
		"source_asset_id": source,
		"language_code":   ctx.Inputs.String("language"),
		"diarize":         false,
		"corrections":     corrections,
	}, cookbook.WithRetries(1))
	if err != nil {
		return err
	}
	_ = ctx.Capture(1, sourceTranscript.URL("txt_asset_url"))

	trim, err := ctx.RunPipeline("video-trim", cookbook.Inputs{
		"video_url":             source,
		"instructions":          instructions,
		"max_clips":             ctx.Inputs.Int("max_clips"),
		"max_clip_duration_sec": ctx.Inputs.Int("max_clip_duration_sec"),
	}, cookbook.WithRetries(1))
	if err != nil {
		return err
	}

	var selected []string
	if strings.HasPrefix(trim.RunID, "dry-") {
		for i := int64(0); i < ctx.Inputs.Int("max_clips"); i++ {
			selected = append(selected, fmt.Sprintf("dry://%s/video_urls/%d", trim.RunID, i+1))
		}
	} else if raw, ok := trim.Output["video_urls"]; ok {
		encoded, marshalErr := json.Marshal(raw)
		if marshalErr == nil {
			marshalErr = json.Unmarshal(encoded, &selected)
		}
		if marshalErr != nil {
			return fmt.Errorf("video-trim returned invalid video_urls: %w", marshalErr)
		}
	}
	if len(selected) == 0 {
		return fmt.Errorf("video-trim returned no clips")
	}
	if int64(len(selected)) > ctx.Inputs.Int("max_clips") {
		return fmt.Errorf("video-trim returned %d clips, above --max-clips %d", len(selected), ctx.Inputs.Int("max_clips"))
	}
	for i, url := range selected {
		if strings.TrimSpace(url) == "" {
			return fmt.Errorf("video-trim returned an empty URL for clip %d", i+1)
		}
	}
	_ = ctx.Capture(2, selected[0])
	ctx.Logf("video-trim selected %d clip%s", len(selected), pluralS(len(selected)))

	limit := int(ctx.Inputs.Int("parallelism"))
	finalURLs := make([]string, len(selected))
	g, gctx := errgroup.WithContext(ctx.Ctx())
	g.SetLimit(limit)
	for i, selectedURL := range selected {
		i, selectedURL := i, selectedURL
		g.Go(func() error {
			sub := ctx.Substep(i + 1).WithContext(gctx)
			reframed, err := sub.RunPipeline("video-reframe", cookbook.Inputs{
				"video_url":           selectedURL,
				"target_aspect_ratio": ctx.Inputs.String("aspect_ratio"),
			}, cookbook.WithRetries(1))
			if err != nil {
				return fmt.Errorf("clip %d reframe: %w", i+1, err)
			}
			reframedURL := reframed.URL("video_url")
			_ = sub.Capture(3, reframedURL)

			transcript, err := sub.RunPipeline("transcription", cookbook.Inputs{
				"source_asset_id": reframedURL,
				"language_code":   ctx.Inputs.String("language"),
				"diarize":         false,
				"corrections":     corrections,
			}, cookbook.WithRetries(1))
			if err != nil {
				return fmt.Errorf("clip %d transcription: %w", i+1, err)
			}
			_ = sub.Capture(4, transcript.URL("txt_asset_url"))

			captioned, err := sub.RunPipeline("captions", cookbook.Inputs{
				"source_asset_id":     reframedURL,
				"transcript_asset_id": transcript.URL("srt_asset_url"),
				"preset_name":         ctx.Inputs.String("preset"),
				"position":            ctx.Inputs.String("position"),
				"subject_y_pct":       reframed.Float("subject_y_pct", 0),
			}, cookbook.WithRetries(1))
			if err != nil {
				return fmt.Errorf("clip %d captions: %w", i+1, err)
			}
			finalURLs[i] = captioned.URL("video_url")
			_ = sub.Capture(5, finalURLs[i])
			sub.Logf("clip %d ready: %s", i+1, finalURLs[i])
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	out, _ := json.Marshal(finalURLs)
	ctx.SetOutput(string(out))
	return nil
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
