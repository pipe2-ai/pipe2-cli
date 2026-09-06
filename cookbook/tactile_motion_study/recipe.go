// Package tactile_motion_study turns one material interaction into a short,
// silent vertical motion study with a verified verbatim title.
package tactile_motion_study

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/pipe2-ai/pipe2-cli/cookbook"
)

func init() {
	cookbook.Register(&Recipe{})
}

type Recipe struct{}

func (r *Recipe) Manifest() cookbook.Manifest {
	minSeconds, maxSeconds := 4.0, 15.0
	return cookbook.Manifest{
		Slug:           "tactile-motion-study",
		Title:          "Material + deformation → tactile motion study",
		Description:    "Generate a text-free material keyframe, animate one controlled MiniMax H3 deformation without audio, and finish with a verified verbatim title overlay.",
		IntroVoiceover: "A single generative prompt has to invent the material, movement, camera, and typography at once, so small tactile clips often drift or grow broken text. Lock the material into a clean opening frame, animate one physical beat, then add the title after the motion is finished.",
		Category:       "concept",
		Tags:           []string{"cli", "pipelines", "tactile", "surreal-asmr", "minimax-h3", "vertical-video", "motion-study", "text-card"},
		Audience:       []string{"creator", "motion designer", "social team", "AI agent"},
		SeeAlso:        []string{"dance-reel"},
		PublishedAt:    "2026-09-01",
		UpdatedAt:      "2026-09-01",
		Inputs: []cookbook.Input{
			{Name: "material", Type: cookbook.String, Default: "pearlescent foam pebble", CLIArg: "--material",
				Description: "The material sample shown in the opening frame and preserved through the motion."},
			{Name: "action", Type: cookbook.String, Default: "compressed once by a smooth porcelain stamp, rebounds, and releases tiny iridescent bubbles", CLIArg: "--action",
				Description: "One visible deformation with a clear start, response, and settled finish."},
			{Name: "title", Type: cookbook.String, Default: "SOFT MATTER STUDY", CLIArg: "--title",
				Description: "The exact title burned into the final video after generation."},
			{Name: "aspect_ratio", Type: cookbook.Enum, Default: "9:16", Values: []string{"9:16", "16:9", "1:1", "4:3", "3:4"}, CLIArg: "--ratio",
				Description: "Frame shape for the keyframe, animation, and final title treatment."},
			{Name: "seconds", Type: cookbook.Int, Default: int64(5), Min: &minSeconds, Max: &maxSeconds, CLIArg: "--seconds",
				Description: "MiniMax H3 clip duration from 4 to 15 seconds."},
		},
		Chain: []cookbook.ChainStep{
			{Pipeline: "image-generator", ArtifactKind: cookbook.Image,
				WhatItDoes: "Creates a text-free opening keyframe with the material centered and the interaction staged just before contact.",
				With: map[string]any{
					"model":   "gemini-3-1-flash-image",
					"quality": "fast",
				}},
			{Pipeline: "video-generator", ArtifactKind: cookbook.Video,
				WhatItDoes: "MiniMax H3 preserves the keyframe while performing one controlled, silent deformation in a locked macro shot.",
				With: map[string]any{
					"audio":      false,
					"duration":   "${inputs.seconds}",
					"model":      "minimax-h3",
					"resolution": "768p",
				}},
			{Pipeline: "text-card", ArtifactKind: cookbook.Video,
				WhatItDoes: "Burns the supplied title into the finished clip and verifies that every character was preserved."},
		},
		ExampleCommand: "pipe2 recipe run tactile-motion-study",
		AgentPrompt:    "Run the pipe2 tactile-motion-study recipe with the pipe2 CLI. Use the defaults for a five-second vertical sample, or set --material, --action, --title, --ratio, and --seconds. Report the final video URL.",
	}
}

func (r *Recipe) Run(ctx *cookbook.Context) error {
	material := ctx.Inputs.String("material")
	action := ctx.Inputs.String("action")
	title := ctx.Inputs.String("title")
	ratio := ctx.Inputs.String("aspect_ratio")
	seconds := ctx.Inputs.Int("seconds")

	keyframe, err := ctx.RunPipeline("image-generator", cookbook.Inputs{
		"model":          "gemini-3-1-flash-image",
		"quality":        "fast",
		"aspect_ratio":   ratio,
		"enhance_prompt": false,
		"prompt": "Text-free opening keyframe for a tactile material study. Hero material: " + material + ". " +
			"Center one clearly readable sample on a matte charcoal surface. Stage the following action at the instant before it starts, without showing its result: " + action + ". " +
			"Locked macro camera, shallow depth of field, soft directional studio light, restrained neutral background, realistic material detail. " +
			"No words, letters, numbers, labels, logos, borders, or watermarks.",
	})
	if err != nil {
		return err
	}
	keyframeURL := keyframe.URL("image_url")
	_ = ctx.Capture(1, keyframeURL)

	motion, err := ctx.RunPipeline("video-generator", cookbook.Inputs{
		"model": "minimax-h3",
		"prompt": "Use the supplied opening image as the exact first frame. Single continuous locked-off macro shot. " +
			"Perform exactly one controlled action: " + action + ". " +
			"Hold briefly before the action, complete it once, then let the material settle. Preserve the material identity, object count, palette, surface, lighting, background, and camera position from the first frame. " +
			"No cuts, camera movement, secondary action, new objects, anatomy, visible text, letters, logos, captions, or sound.",
		"start_image":    keyframeURL,
		"aspect_ratio":   ratio,
		"duration":       strconv.FormatInt(seconds, 10),
		"resolution":     "768p",
		"enhance_prompt": false,
		"audio":          false,
	})
	if err != nil {
		return err
	}
	motionURL := motion.URL("video_url")
	_ = ctx.Capture(2, motionURL)

	card, err := ctx.RunPipeline("text-card", cookbook.Inputs{
		"text":           title,
		"background_url": motionURL,
		"instructions": "Render the supplied title verbatim: preserve every character, space, and uppercase letter. Do not correct, rephrase, split, or add a subtitle. " +
			"Use centered bold white sans-serif type in the upper third with a restrained black shadow, no box, a short fade in, and keep it visible through the final frame.",
	})
	if err != nil {
		return err
	}
	if err := requireExactTitle(card, title); err != nil {
		return err
	}
	finalURL := card.URL("video_url")
	_ = ctx.Capture(3, finalURL)

	ctx.SetOutput(finalURL)
	ctx.Logf("tactile motion study ready: %s", finalURL)
	return nil
}

func requireExactTitle(result *cookbook.Result, title string) error {
	if strings.HasPrefix(result.RunID, "dry-") {
		return nil
	}
	meta, ok := result.Output["meta"].(map[string]any)
	if !ok {
		return fmt.Errorf("text-card did not return title verification metadata")
	}
	raw, ok := meta["drawtext_params"].(string)
	if !ok {
		return fmt.Errorf("text-card did not return drawtext parameters")
	}
	var rendered struct {
		Text     string `json:"text"`
		Subtitle string `json:"subtitle"`
	}
	if err := json.Unmarshal([]byte(raw), &rendered); err != nil {
		return fmt.Errorf("text-card returned invalid drawtext parameters: %w", err)
	}
	if rendered.Text != title || rendered.Subtitle != "" {
		return fmt.Errorf("text-card changed the exact title: got %q with subtitle %q", rendered.Text, rendered.Subtitle)
	}
	return nil
}
