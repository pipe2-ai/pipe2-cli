// Package hidden_world_reveal opens an ordinary container onto an impossible
// miniature world, using matching opening and payoff frames to anchor the reveal.
package hidden_world_reveal

import (
	"strconv"

	"github.com/pipe2-ai/pipe2-cli/cookbook"
)

func init() { cookbook.Register(&Recipe{}) }

type Recipe struct{}

func (r *Recipe) Manifest() cookbook.Manifest {
	minSeconds, maxSeconds := 6.0, 12.0
	return cookbook.Manifest{
		Slug:           "hidden-world-reveal",
		Title:          "An ordinary box opens onto a tiny impossible world",
		Description:    "Create a silent reveal of a tiny moonlit ocean inside a hinged wooden box: build the open scene, close its lid with an image edit, then animate the discovery.",
		IntroVoiceover: "The blue light in the gap is the question. The tiny ocean inside is the answer. Build that world first, then hide it beneath the same lid. The animation uncovers a place that already exists instead of inventing one as the box opens.",
		Category:       "concept",
		Tags:           []string{"reveal", "miniature-world", "surreal", "vertical-video", "short-form"},
		Audience:       []string{"creator", "motion designer", "social team"},
		SeeAlso:        []string{"tactile-motion-study"},
		PublishedAt:    "2026-09-05",
		UpdatedAt:      "2026-09-06",
		Inputs: []cookbook.Input{
			{Name: "container", Type: cookbook.String, Default: "a small deep unpainted wooden box with one rear-hinged lid", CLIArg: "--container",
				Description: "One recognizable container with a simple opening and room for a miniature scene."},
			{Name: "world", Type: cookbook.String, Default: "a miniature moonlit ocean with one tiny ivory lighthouse on a dark rock and a small curling wave", CLIArg: "--world",
				Description: "The hidden interior, with one large readable feature rather than many tiny details."},
			{Name: "opening", Type: cookbook.String, Default: "the solid lid rotates upward around its fixed rear hinge to a nearly vertical open position while the box and its contents remain stationary", CLIArg: "--opening",
				Description: "One mechanical opening motion that fits the container, such as a drawer sliding or a lid lifting."},
			{Name: "aspect_ratio", Type: cookbook.Enum, Default: "9:16", Values: []string{"9:16", "16:9", "1:1"}, CLIArg: "--ratio",
				Description: "Frame shape shared by both stills and the finished reveal."},
			{Name: "seconds", Type: cookbook.Int, Default: int64(8), Min: &minSeconds, Max: &maxSeconds, CLIArg: "--seconds",
				Description: "Six to twelve seconds; the opening finishes before the final two-second payoff hold."},
		},
		Chain: []cookbook.ChainStep{
			{Pipeline: "image-generator", ArtifactKind: cookbook.Image,
				WhatItDoes: "Builds the open container and its complete hidden world first, with room for the moving part to close over it.",
				With:       map[string]any{"model": "gpt-image-2", "quality": "auto"}},
			{Pipeline: "image-editor", ArtifactKind: cookbook.Image,
				WhatItDoes: "Closes the same container over the existing world, leaving a blue-lit gap without changing the fixed geometry.",
				With:       map[string]any{"model": "gemini-3-1-flash-image"}},
			{Pipeline: "video-generator", ArtifactKind: cookbook.Video,
				WhatItDoes: "Connects the opening and payoff frames with one continuous opening motion, then holds the visible world for two seconds. The result is deliberately silent.",
				With:       map[string]any{"model": "minimax-h3", "duration": "${inputs.seconds}", "resolution": "768p", "audio": false}},
		},
		ExampleCommand: "pipe2 recipe run hidden-world-reveal",
		AgentPrompt:    "Run the pipe2 hidden-world-reveal recipe. Defaults create an eight-second silent vertical reveal of a tiny ocean inside a hinged wooden box. Customize --container, --world, and --opening together to create another discovery. Report the final video URL.",
	}
}

func (r *Recipe) Run(ctx *cookbook.Context) error {
	container := ctx.Inputs.String("container")
	world := ctx.Inputs.String("world")
	opening := ctx.Inputs.String("opening")
	ratio := ctx.Inputs.String("aspect_ratio")
	seconds := ctx.Inputs.Int("seconds")

	end, err := ctx.RunPipeline("image-generator", cookbook.Inputs{
		"model": "gpt-image-2", "quality": "auto", "aspect_ratio": ratio, "enhance_prompt": false,
		"prompt": "Photorealistic final frame of a hidden-world reveal. One container: " + container + ". " +
			"On a warm oak desk, show the completed opening action: " + opening + ". Inside is " + world + ". " +
			"The interior is a real place with photographic depth, irregular rocks and natural water, not a resin diorama, painted backdrop or toy. Use broken ripples and foam, not a sculpted frozen curling wave. " +
			"Mechanical plausibility is essential: for a sliding box, show one rigid outer sleeve with a solid roof and one deep tray extended almost its full length toward the camera, still engaged inside the sleeve at the back. The whole miniature world sits below the tray rim, low enough to slide under the roof without collision. Its focal landmark is in the front half of the tray, fully exposed and readable. " +
			"Leave generous headroom: the top of the tallest landmark is far BELOW the top of the wooden side walls, with visible empty clearance above it. Make the tray deep and its walls tall; recess the entire seascape into its bottom. Nothing projects above the rim, even the lighthouse lantern. " +
			"For a hinged box, the rigid lid is upright at the rear hinge, exactly the same width and depth as the box opening, and can close flat over it. The whole world stays fixed within the box below its rim. " +
			"The ocean and its horizon exist deep below the wooden rim; the exterior desk stays ordinary. No upright sky panel or moon painted onto a wooden wall. Cool light from the interior softly illuminates the inner wood. " +
			"Locked three-quarter overhead close-up, high enough to see inside when it opens. Keep the whole container and its opening path inside the central 70% of the frame. " +
			"Soft warm side light, realistic wood grain, quiet defocused background, crisp object edges. " +
			"No hands, people, words, letters, numbers, labels, logos, watermarks, or borders.",
	})
	if err != nil {
		return err
	}
	endURL := end.URL("image_url")
	if err := ctx.Capture(1, endURL); err != nil {
		return err
	}

	start, err := ctx.RunPipeline("image-editor", cookbook.Inputs{
		"model": "gemini-3-1-flash-image", "image_urls": []string{endURL}, "aspect_ratio": ratio, "enhance_prompt": false,
		"instructions": "Derive the opening frame by mechanically closing this exact container over the world already inside. Reverse this action: " + opening + ". " +
			"Move only the intended moving part. For a sliding drawer, translate the entire rigid tray including its front, side walls and all contents backward into the sleeve until only a narrow blue-lit gap remains. Keep the entire outer sleeve, roof, mouth and side walls at exactly the same image coordinates and size. " +
			"For a hinged box, rotate only the solid lid downward around the same rear hinge until it is nearly closed, leaving a narrow blue-lit gap at the front. Keep the box body and every part of the world at exactly the same coordinates; the solid lid occludes them. The lid retains its exact dimensions and thickness. " +
			"The world remains identical at the same scale within the container, hidden by its solid cover, never erased, flattened, resized or transformed. Show just a sliver of real water and blue light through the gap; the focal landmark is physically occluded by the cover. " +
			"Preserve desk grain, camera, crop, lighting, background and every stationary pixel. Do not shorten or stretch the box, move its roof, add rails, replace it with a different lid, or move the camera. " +
			"No floating pieces, duplicated containers, spilling contents, hands, people, words, letters, numbers, labels, logos, watermarks, or borders.",
	})
	if err != nil {
		return err
	}
	startURL := start.URL("image_url")
	if err := ctx.Capture(2, startURL); err != nil {
		return err
	}

	video, err := ctx.RunPipeline("video-generator", cookbook.Inputs{
		"model": "minimax-h3", "start_image": startURL, "end_image": endURL,
		"aspect_ratio": ratio, "duration": strconv.FormatInt(seconds, 10), "resolution": "768p", "audio": false, "enhance_prompt": false,
		"prompt": "Start at the supplied opening frame and finish at the supplied payoff frame. Single continuous locked-camera close-up of " + container + ". " +
			"The blue-lit gap is visible from the first frame. Begin opening immediately: " + opening + ". " +
			"Progressively uncover " + world + ", already present at final size inside the container from the first frame. For a hinged box, the complete world stays fixed while only the lid rotates upward, physically uncovering it. For a drawer, the entire world moves rigidly with the tray and is revealed only as it crosses the stationary roof edge. Keep the landmark's scale constant: do not grow it, fade it in, raise it through the floor, or dissolve a picture onto the wood. " +
			"Complete the opening by second " + strconv.FormatInt(seconds-2, 10) + ", then hold the discovery clearly in view for the final two seconds. " +
			"Keep natural water ripples and foam moving within the existing ocean, with photographic depth rather than frozen resin. Preserve the container geometry, scale, desk, camera, lighting, and background throughout. " +
			"Only the intended moving part moves. For a drawer, its outer sleeve, roof, side walls and front opening stay rigid and stationary at their starting positions while the inner tray slides forward. " +
			"Keep the contents within the container and the complete reveal in frame. End open; do not close it again or reverse the action. " +
			"No camera moves, cuts, dissolves, melting, new objects outside the container, hands, people, text, logos, or sound.",
	})
	if err != nil {
		return err
	}
	finalURL := video.URL("video_url")
	if err := ctx.Capture(3, finalURL); err != nil {
		return err
	}
	ctx.SetOutput(finalURL)
	return nil
}
