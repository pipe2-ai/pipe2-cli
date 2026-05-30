## Why it works

The grid is a storyboard. `image-generator` renders 16 labeled dance
poses in a 4×4 layout, and Seedance reads it as a reference image —
following the cells in sequence instead of guessing motion frame by
frame. A named move goes in; the same move comes out.

The music is composed *before* the dance, not after. Seedance
beat-conditions the clip on the finished track, so the choreography
lands on the rhythm instead of drifting against a bed layered on later.

## The knobs that matter

- **`moves`** — the cheapest lever on quality. Named moves ("moonwalk,
  body roll, donkey kick") beat generic adjectives: each name becomes a
  labeled grid cell, and Seedance reads those labels.
- **`dance_style`** — sets the vibe for both the grid and the dance
  ("K-pop solo choreography", "classical ballet"). Keep it specific;
  "dance" is not a style.
- **`subject`** / **`persona`** — `subject` is who's dancing and what
  they're wearing; `persona` anchors a recurring identity so the same
  face and outfit hold across the clip. Specific attire and setting read
  better than a generic body.

Duration, music mood, watermark, and aspect ratio all have sensible
defaults — see the manifest for the full set.

## Notes

The bring-your-own inputs come in pairs: a text flag generates the
asset, the `-url` variant supplies your own. `persona` / `persona-url`,
music mood / `music-url`, watermark prompt / `watermark-url` — prompt to
generate it, URL to bring it. A brand with a fixed logo or a curated
track uses the `-url` side.
