## Why it works

The opening frame is a material contract. It fixes the sample, tool, lighting, and camera before motion generation starts, so MiniMax H3 can spend its short shot on one physical response instead of inventing the whole scene while it moves. A short still hold makes the first contact legible. A settled final beat lets the clip end cleanly.

Typography comes after the generative motion. That separation keeps stray letters out of the material image and stops the video model from warping a title during deformation. The final title is burned onto the completed clip, and the recipe rejects the result if the text treatment changes any supplied character or invents a subtitle.

## The knobs that matter

- **`material`** — name a material with a clear surface response, such as foam, gel, wax, clay, or woven fibers.
- **`action`** — describe one force, one response, and one settled finish; a single readable beat is more stable than a sequence.
- **`seconds`** — use five seconds for one compact interaction and add time only when the material needs a slower rebound.
- **`title`** — keep it short enough to read at phone size; its capitalization and characters are preserved verbatim.

See the manifest for framing and the full input set.

## Notes

Keep `action` literal. Camera moves, scene changes, or a second deformation compete with the material response and make the result harder to loop. If the opening image already shows the end state, simplify the action wording so “immediately before contact” has only one interpretation.
