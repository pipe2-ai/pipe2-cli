// Package product_world_tour carries one original product through three campaign sets.
package product_world_tour

import "github.com/pipe2-ai/pipe2-cli/cookbook"

func init() { cookbook.Register(&Recipe{}) }

type Recipe struct{}

func (*Recipe) Manifest() cookbook.Manifest {
	return cookbook.Manifest{
		Slug: "product-world-tour", Title: "One product, three worlds: a match-cut campaign",
		Description:    "Design an original product, restage it in two contrasting worlds, then turn the three connected images into a nine-second vertical campaign.",
		IntroVoiceover: "The scenery changes. The product stays familiar. Matching its silhouette and position gives each cut a visual anchor, so three imaginary sets read as one campaign instead of three unrelated generations.",
		Category:       "concept", Tags: []string{"product", "match-cut", "campaign", "vertical-video"}, Audience: []string{"designer", "creator", "social team"},
		SeeAlso: []string{"hidden-world-reveal", "tactile-motion-study"}, PublishedAt: "2026-09-06", UpdatedAt: "2026-09-06",
		Inputs: []cookbook.Input{
			{Name: "product", Type: cookbook.String, Default: "one unbranded cobalt-blue sculptural perfume bottle, a squat rounded rectangular opaque glass body with a single large polished silver sphere cap", CLIArg: "--product", Description: "An original product concept with a simple, distinctive silhouette; avoid labels and tiny markings."},
			{Name: "scene_one", Type: cookbook.String, Default: "on a pale peach sand dune, surrounded by three enormous curved cobalt-blue petals under a warm apricot sky", CLIArg: "--scene-one", Description: "The immediately visible opening set."},
			{Name: "scene_two", Type: cookbook.String, Default: "on a flat mirror-black stone in a lush emerald forest, surrounded by giant wet fern fronds and soft drifting mist", CLIArg: "--scene-two", Description: "A contrasting environment, preserving the original product and composition."},
			{Name: "scene_three", Type: cookbook.String, Default: "on a white salt-flat pedestal above a sea of clouds at blue hour, with one huge luminous pale moon behind it", CLIArg: "--scene-three", Description: "The most expansive final set, held clearly for the payoff."},
		},
		Chain: []cookbook.ChainStep{
			{Pipeline: "image-generator", ArtifactKind: cookbook.Image, WhatItDoes: "Designs the product in its opening campaign set, establishing the silhouette, camera and framing.", With: map[string]any{"model": "gpt-image-2", "quality": "auto"}},
			{Pipeline: "image-editor", ArtifactKind: cookbook.Image, WhatItDoes: "Uses the original image to place the same product in the second set without redesigning it.", With: map[string]any{"model": "gemini-3-1-flash-image"}},
			{Pipeline: "image-editor", ArtifactKind: cookbook.Image, WhatItDoes: "Returns to the original product reference for the final set, avoiding accumulated changes from successive edits.", With: map[string]any{"model": "gemini-3-1-flash-image"}},
			{Pipeline: "video-generator", ArtifactKind: cookbook.Video, WhatItDoes: "Animates the three ordered references with hard match cuts, subtle environmental motion and an unhurried final hold. The campaign is intentionally silent.", With: map[string]any{"model": "seedance-2-5", "duration": "9", "resolution": "720p", "audio": false}},
		},
		ExampleCommand: "pipe2 recipe run product-world-tour",
		AgentPrompt:    "Run the pipe2 product-world-tour recipe to create an original perfume concept in three imaginary campaign sets. Customize --product and --scene-one, --scene-two, --scene-three together for another concept. Report the final video URL and the three reference images.",
	}
}

func (*Recipe) Run(ctx *cookbook.Context) error {
	first, err := ctx.RunPipeline("image-generator", cookbook.Inputs{
		"model": "gpt-image-2", "quality": "auto", "aspect_ratio": "9:16", "enhance_prompt": false,
		"prompt": "Premium surreal product campaign photograph. Product: " + ctx.Inputs.String("product") + ". Set: " + ctx.Inputs.String("scene_one") + ". " +
			"One single complete product centered at x=50%, y=54%, occupying roughly 38% of frame height. Front three-quarter view, level camera, full product and cap visible, clean continuous silhouette, generous headroom. The scene feels enormous around this clearly readable foreground object. Tangible materials, realistic reflections and contact shadow, exquisite side lighting, restrained editorial composition. No people, hands, duplicate products, text, letters, logos, labels, watermarks or borders.",
	})
	if err != nil {
		return err
	}
	refs := []string{first.URL("image_url")}
	if err := ctx.Capture(1, refs[0]); err != nil {
		return err
	}
	for i, name := range []string{"scene_two", "scene_three"} {
		edited, err := ctx.RunPipeline("image-editor", cookbook.Inputs{
			"model": "gemini-3-1-flash-image", "image_urls": []string{refs[0]}, "aspect_ratio": "9:16", "enhance_prompt": false,
			"instructions": "Restage the exact single product in this reference photograph. New set: " + ctx.Inputs.String(name) + ". " +
				"Preserve the product's exact design, silhouette, proportions, color, cap, camera angle, position and apparent size. Replace the environment and supporting surface completely, adapting only physically plausible reflections, illumination and contact shadows on the unchanged product. Product remains centered and fully visible. Premium surreal campaign photography, realistic tangible materials, clean edges, depth and atmosphere. Do not preserve elements from the old background. No people, hands, extra products, redesigns, added parts, text, letters, labels, logos, watermarks or borders.",
		})
		if err != nil {
			return err
		}
		refs = append(refs, edited.URL("image_url"))
		if err := ctx.Capture(i+2, refs[i+1]); err != nil {
			return err
		}
	}
	video, err := ctx.RunPipeline("video-generator", cookbook.Inputs{
		"model": "seedance-2-5", "reference_images": refs, "duration": "9", "resolution": "720p", "aspect_ratio": "9:16", "audio": false, "enhance_prompt": false,
		"prompt": "A nine-second vertical surreal product campaign, exactly three shots in the supplied reference order. Image 1 is shot one, image 2 is shot two, image 3 is shot three. All depict the SAME single product. " +
			"0-3 seconds: show the complete first reference composition immediately, with subtle atmospheric movement in its environment. At exactly 3 seconds HARD MATCH CUT to reference image 2. 3-6 seconds: show the second environment, with subtle atmospheric movement. At exactly 6 seconds HARD MATCH CUT to reference image 3. 6-9 seconds: show the final expansive set, atmospheric movement, hold the complete product clearly to the end. " +
			"Product remains rigid, unchanged in design and color, centered at the same position and scale in all three shots. Locked camera in each shot. Motion belongs to surrounding petals, mist or clouds, never to the bottle or its cap. Preserve the references' clean silhouette and realistic material detail. Cuts replace the entire environment instantly: no dissolves, morphs, wipes, blending, warping or intermediate worlds. Exactly one product per shot. No spinning, opening, melting, growing, added parts, new props, text, logos, people, voice, music or sound. Intentional silence.",
	})
	if err != nil {
		return err
	}
	url := video.URL("video_url")
	if err := ctx.Capture(4, url); err != nil {
		return err
	}
	ctx.SetOutput(url)
	return nil
}
