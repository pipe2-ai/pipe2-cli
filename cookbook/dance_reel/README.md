## Why it works

The grid is a storyboard. `image-generator` renders 16 labeled dance
poses in a 4×4 layout, and Seedance reads it as a reference image: following the cells in sequence instead of guessing motion frame by
frame. A named move goes in; the same move comes out.

The music is composed *before* the dance, not after. Seedance uses the
finished track as a timing reference, so the choreography lands on the
rhythm instead of feeling detached from the soundtrack.

## The knobs that matter

- **`moves`**: the cheapest lever on quality. Named moves ("moonwalk,
  body roll, donkey kick") beat generic adjectives: each name becomes a
  labeled grid cell, and Seedance reads those labels.
- **`dance_style`**: sets the vibe for both the grid and the dance
  ("K-pop solo choreography", "classical ballet"). Keep it specific;
  "dance" is not a style.
- **`subject`** / **`persona`**: `subject` is who's dancing and what
  they're wearing; `persona` anchors a recurring identity so the same
  face and outfit hold across the clip. Specific attire and setting read
  better than a generic character.

Duration, music mood, watermark, and aspect ratio all have sensible
defaults; see the recipe options for the full set.

## Notes

You can generate each supporting asset or supply one you already have.
Use `persona` or `persona-url` for identity, music mood or `music-url`
for the soundtrack, and watermark prompt or `watermark-url` for the
logo. A brand with a fixed logo or a curated track should use the URL
option.
