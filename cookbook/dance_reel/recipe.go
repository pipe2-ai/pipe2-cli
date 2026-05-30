// Package dance_reel implements the dance-reel cookbook recipe: one
// prompt → a vertical dance video reel built entirely on-platform.
//
// The chain demonstrates the GPT Image 2 + Seedance 2 Pro pairing
// described in the article — generate a "moves grid" reference sheet,
// hand it to Seedance as a multimodal anchor, then stitch a 3-second
// ken-burns reveal of the grid in front of the dance clip with an
// Eleven Music bed.
//
// Source of truth for the article at pipe2.ai/learn/dance-reel.
package dance_reel

import (
	"strconv"

	"github.com/pipe2-ai/pipe2-cli/cookbook"
)

func init() {
	cookbook.Register(&Recipe{})
}

type Recipe struct{}

func (r *Recipe) Manifest() cookbook.Manifest {
	min4, max15 := 4.0, 15.0
	return cookbook.Manifest{
		Slug:           "dance-reel",
		Title:          "One prompt → a vertical AI dance reel",
		Description:    "Generate a labeled dance-move grid with GPT Image 2, hand it to Seedance 2 Pro as a reference so the choreography actually follows the moves, then stitch it into a music-synced vertical reel.",
		IntroVoiceover: "Most AI dance videos look like slop because the model is guessing the moves frame by frame. The fix is to pre-author a movement grid with GPT Image 2 and pass it to Seedance 2 Pro as a reference image — now it has a storyboard to follow. This recipe chains the grid, the dance, and a matching music bed into one reel.",
		Category:       "tutorial",
		Tags:           []string{"cli", "claude-code", "pipelines", "dance", "seedance", "gpt-image-2", "reel", "automation"},
		Audience:       []string{"creator", "agency editor", "AI agent", "marketer"},
		SeeAlso:        []string{"clip-factory"},
		PublishedAt:    "2026-05-11",
		UpdatedAt:      "2026-05-11",
		Inputs: append([]cookbook.Input{
			{Name: "subject", Type: cookbook.String, Default: "breakdancer in baggy neon techwear", CLIArg: "--subject",
				Description: "Who's dancing. Used in both the grid render and the Seedance prompt."},
			{Name: "persona", Type: cookbook.String, Default: "", CLIArg: "--persona",
				Description: "Optional persona description (e.g. 'red-haired fashion-forward 21-year-old influencer in pink avant-garde outfit'). When set, generates a multi-angle character sheet first and passes it to Seedance as a second reference image so the dance preserves identity across frames."},
			{Name: "persona_url", Type: cookbook.AssetURL, Default: "", CLIArg: "--persona-url",
				Description: "Pre-existing persona reference (URL or local path — same shape as --music-url / --watermark-url). When set, image-generator is skipped and this image goes directly to Seedance as the identity anchor. Use for recurring brand mascots or when you already have a character sheet you trust."},
			{Name: "dance_style", Type: cookbook.String, Default: "K-pop solo choreography", CLIArg: "--style",
				Description: `Genre/style label — e.g. "K-pop solo choreography", "urban hip-hop", "classical ballet".`},
			{Name: "moves", Type: cookbook.String, Default: "moonwalk, body roll, donkey kick, electric slide", CLIArg: "--moves",
				Description: "Comma-separated list of named moves Seedance should perform in order."},
			{Name: "aspect_ratio", Type: cookbook.Enum, Default: "9:16", CLIArg: "--ratio",
				Values:      []string{"9:16", "16:9", "1:1"},
				Description: "Output aspect ratio. 9:16 for TikTok/Reels, 16:9 for landscape."},
			{Name: "dance_seconds", Type: cookbook.Int, Default: int64(8), Min: &min4, Max: &max15, CLIArg: "--dance-seconds",
				Description: "Duration of the Seedance segment. 4-15s; total reel is +3s for the grid reveal."},
			{Name: "music_mood", Type: cookbook.String, Default: "K-pop dance anthem at 128 BPM. Punchy four-on-the-floor kick, sidechained glossy synth lead, glittery hi-hats, percussive synth stab on the off-beats. Full energy from bar one, no intro.", CLIArg: "--music",
				Description: "Mood prompt for the Eleven Music bed."},
			{Name: "music_url", Type: cookbook.AssetURL, Default: "", CLIArg: "--music-url",
				Description: "Pre-existing music file (URL or local path). When set, music-generator is skipped and this track is used directly."},
		}, cookbook.WatermarkInputs()...),
		Chain: []cookbook.ChainStep{
			{Pipeline: "image-generator", ArtifactKind: cookbook.Image,
				WhatItDoes: "GPT Image 2 renders a 16-panel dance move reference grid — one labeled pose per cell.",
				With:       map[string]any{"model": "gpt-image-2"}},
			{Pipeline: "music-generator", ArtifactKind: cookbook.Audio,
				WhatItDoes: "Eleven Music composes a vocal-led K-pop dance bed. Generated FIRST so the dance can be choreographed to its rhythm.",
				With: map[string]any{
					"model":        "eleven-music-v1",
					"duration_sec": "${inputs.dance_seconds}",
					"vocals":       true,
				}},
			{Pipeline: "image-motion", ArtifactKind: cookbook.Video,
				WhatItDoes: "Claude designs a ken-burns pan over the grid so it reads on screen before the dance starts."},
			{Pipeline: "video-generator", ArtifactKind: cookbook.Video,
				WhatItDoes: "Seedance 2 Pro generates the dance clip. The grid tags as @image1 for choreography, the music tags as @audio1 for beat-conditioning — choreography is synced to the actual track.",
				With: map[string]any{
					"model":        "seedance-2-0-pro",
					"duration_sec": "${inputs.dance_seconds}",
					"resolution":   "720p",
				}},
			{Pipeline: "video-reel", ArtifactKind: cookbook.Video,
				WhatItDoes: "Claude stitches grid-reveal + dance with a snappy crossfade and the music bed."},
			cookbook.WatermarkChainStep(),
		},
		ExampleCommand: "pipe2 recipe run dance-reel",
		AgentPrompt:    "Run the pipe2 dance-reel recipe via the pipe2 CLI. Defaults produce a K-pop breakdancer reel with a vocal-led Eleven Music bed — pass --subject, --style, --moves, --music to customize, or --persona / --watermark-url for branded output. Report the final video URL when done.",
	}
}

func (r *Recipe) Run(ctx *cookbook.Context) error {
	subject := ctx.Inputs.String("subject")
	persona := ctx.Inputs.String("persona")
	style := ctx.Inputs.String("dance_style")
	moves := ctx.Inputs.String("moves")
	ratio := ctx.Inputs.String("aspect_ratio")
	danceSec := ctx.Inputs.Int("dance_seconds")
	musicMood := ctx.Inputs.String("music_mood")

	// 0. Optional persona identity reference. Three modes mirror the
	//    music pattern:
	//   (a) --persona-url supplied → use that file/URL directly as the
	//       identity anchor; skip image-generator. For recurring
	//       characters or curated reference sheets you already trust.
	//   (b) --persona text supplied → generate a 5-panel character
	//       sheet via GPT Image 2 (hero + 4 angles), then use as anchor.
	//   (c) neither → no persona; Seedance gets only the moves grid
	//       and invents the character.
	var personaURL string
	if supplied := ctx.ResolveAssetURL("persona_url"); supplied != "" {
		personaURL = supplied
		ctx.Logf("↻ persona — using supplied --persona-url, skipping image-generator")
		_ = ctx.Capture(0, personaURL)
	} else if persona != "" {
		personaPrompt := "Character reference sheet for: " + persona + ". " +
			"5-panel layout — one large hero shot in the center showing the full outfit and pose, " +
			"surrounded by 4 head-and-shoulders portraits: 3/4 left, 3/4 right, profile, and back-of-head. " +
			"Studio lighting, neutral background, consistent identity (face, hair, makeup, outfit) across all panels. " +
			"Photorealistic, sharp focus, professional model casting sheet aesthetic."
		personaShot, err := ctx.RunPipeline("image-generator", cookbook.Inputs{
			"model":        "gemini-3-1-flash-image", // multi-angle identity at 1.5cr vs gpt-image-2's 7cr; no text labels needed on the persona sheet so gpt-image-2 is overkill
			"prompt":       personaPrompt,
			"aspect_ratio": "1:1",
		}, cookbook.WithWhatItDoes("Generates a 5-panel character reference sheet so Seedance keeps identity consistent across the dance clip."))
		if err != nil {
			return err
		}
		personaURL = personaShot.URL("image_url")
		_ = ctx.Capture(0, personaURL)
	}

	// 1. Grid render. GPT Image 2 is best at multi-cell layouts with
	//    rendered labels; we hard-pin model=gpt-image-2 because
	//    Imagen/Gemini produce blurry text on the move labels.
	//    Versioned slug per SKILL.md "Model slug naming".
	gridPrompt := "16-panel dance move reference grid, 4 rows by 4 columns, " +
		"each cell shows " + subject + " performing a different move from this list: " + moves + ". " +
		"Style: " + style + ". " +
		"Each cell has the move name labeled in clean bold sans-serif text below the pose. " +
		"Neutral studio background, consistent lighting across cells, professional choreography reference sheet."
	grid, err := ctx.RunPipeline("image-generator", cookbook.Inputs{
		"model":        "gpt-image-2",
		"prompt":       gridPrompt,
		"aspect_ratio": ratio,
	})
	if err != nil {
		return err
	}
	gridURL := grid.URL("image_url")
	_ = ctx.Capture(1, gridURL)

	// 2. Music bed (generated FIRST so the dance can be conditioned on it).
	//    Three modes:
	//   (a) --music-url supplied → use that file directly.
	//   (b) --music is empty/"none" → ship silent reel.
	//   (c) otherwise → call music-generator pinned to eleven-music-v1
	//       with vocals=true. K-pop's identity is in the vocal hook;
	//       /v1/music/detailed handles prompt engineering internally.
	//       Soft-fail on error so a provider hiccup doesn't kill the run.
	var musicURL string
	if supplied := ctx.ResolveAssetURL("music_url"); supplied != "" {
		musicURL = supplied
		ctx.Logf("↻ music-generator — using supplied --music-url, skipping generation")
		_ = ctx.Capture(2, musicURL)
	} else if musicMood != "" && musicMood != "none" {
		musicSec := int(danceSec) + 5
		music, err := ctx.RunPipeline("music-generator", cookbook.Inputs{
			"model":        "eleven-music-v1",
			"mood_prompt":  musicMood,
			"duration_sec": musicSec,
			"vocals":       true,
		})
		if err != nil {
			ctx.Logf("  (music-generator failed: %v — continuing without music)", err)
		} else {
			musicURL = music.URL("audio_url")
			_ = ctx.Capture(2, musicURL)
		}
	} else {
		ctx.Logf("  (skipping music-generator — --music is %q)", musicMood)
	}

	// 3. Grid reveal. 3-second ken-burns clip so the viewer registers
	//    the moves before the dance begins. Claude picks pan/zoom.
	reveal, err := ctx.RunPipeline("image-motion", cookbook.Inputs{
		"image_url":    gridURL,
		"instructions": "Slow pan across the dance move grid, ending on a centered zoom. About 3 seconds.",
	})
	if err != nil {
		return err
	}
	revealURL := reveal.URL("video_url")
	_ = ctx.Capture(3, revealURL)

	// 4. Seedance dance segment, audio-conditioned by the music bed.
	//    Per the Seedance 2.0 prompt guide (WaveSpeed / Atlas / Apiyi):
	//    audio inputs shape video timing/beats — passing the actual
	//    music as `reference_audio_url` makes the dance choreographed
	//    *to* the track, not just stylistically aligned with K-pop in
	//    general. Empirically (test commit history): same prompt with
	//    no audio ref → 5.5 Mbps motion variance, generic dance content;
	//    same prompt with music ref → measurably tighter, on-tempo motion.
	//    Falls back to no-audio-ref when musicURL is empty (--music none
	//    or music-generator soft-failed).
	//
	//    Role tagging via @-mentions per the same guide: tag the grid
	//    explicitly as a choreography reference so the model decomposes
	//    its cells instead of treating it as a generic style anchor.
	refImages := []string{gridURL}
	subjectClause := subject
	choreoRef := "@image1"
	if personaURL != "" {
		refImages = []string{personaURL, gridURL}
		subjectClause = "the character shown in @image1 (preserve face, hair, makeup, and outfit exactly across every frame)"
		choreoRef = "@image2"
	}
	dancePrompt := choreoRef + " is a 16-panel reference grid where each cell shows one labeled dance move. " +
		"Treat " + choreoRef + " as a choreography sequence to follow, not a style anchor. " +
		"Single continuous take: " + subjectClause + " performs the moves shown in " + choreoRef + " in this exact order: " + moves + ". " +
		"Each named move must be visibly executed and recognizable as the labeled move from the grid — don't substitute with generic dance gestures. "
	if musicURL != "" {
		dancePrompt += "Synchronize the choreography to the rhythm and energy of @audio1. "
	}
	dancePrompt += "Style: " + style + ". " +
		"Cinematic 4k, dynamic tracking shot, neon urban street background, high energy, sharp choreography, no slop."
	danceInputs := cookbook.Inputs{
		"model":            "seedance-2-0-pro",
		"prompt":           dancePrompt,
		"aspect_ratio":     ratio,
		"duration":         strconv.FormatInt(danceSec, 10),
		"audio":            false, // Seedance's native audio off — we layer the original music via video-reel so the output audio is the canonical generated track, not a Seedance-resampled version
		"reference_images": refImages,
	}
	if musicURL != "" {
		danceInputs["reference_audio_url"] = musicURL
	}
	dance, err := ctx.RunPipeline("video-generator", danceInputs)
	if err != nil {
		return err
	}
	danceURL := dance.URL("video_url")
	_ = ctx.Capture(4, danceURL)

	// 5. Final stitch. Reveal + dance with crossfade, original music
	//    layered on top (Seedance's reference_audio shapes motion but
	//    doesn't ride through to output — see commit log for the role
	//    distinction). video-reel reads Claude-authored fade levels
	//    from the instructions string.
	reel, err := ctx.RunPipeline("video-reel", cookbook.Inputs{
		"video_urls":    []string{revealURL, danceURL},
		"music_url":     musicURL,
		"instructions":  "Snappy 0.3s crossfade between grid reveal and dance. Full music throughout.",
		"target_aspect": ratio,
	})
	if err != nil {
		return err
	}
	finalURL := reel.URL("video_url")
	_ = ctx.Capture(5, finalURL)

	// 6. Watermark pass — shared helpers across recipes. ResolveWatermark
	//    returns "" when --no-watermark is set; ApplyWatermark is then a
	//    no-op and returns finalURL unchanged.
	watermarkURL, err := cookbook.ResolveWatermark(ctx)
	if err != nil {
		return err
	}
	branded, err := cookbook.ApplyWatermark(ctx, finalURL, watermarkURL, ctx.Inputs.Int("watermark_scale_pct"))
	if err != nil {
		return err
	}
	if branded != finalURL {
		finalURL = branded
		_ = ctx.Capture(6, finalURL)
	}

	ctx.SetOutput(finalURL)
	ctx.Logf("dance reel ready: %s", finalURL)
	return nil
}
