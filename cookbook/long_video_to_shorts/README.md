## Why it works

The recipe first creates a timestamped source transcript, then `video-trim`
reviews the complete source, compares candidate moments against your
instructions, and returns several self-contained clips. There is no separate
highlight list to keep in sync and no timestamp file to prepare.

Every selected clip then gets its own finishing pass. Reframing happens before
transcription, so the transcript and burned captions match the exact final cut.
The branches run in parallel: asking for more clips adds work, but does not turn
the recipe into one long serial queue.

## The knobs that matter

- **`instructions`** — describe both the subject and the editorial test. “Five
  strongest self-contained arguments” is better than “interesting moments.”
- **`max_clips`** — caps the pack without forcing the agent to return weak
  filler when fewer moments satisfy the brief.
- **`max_seconds`** — sets the ceiling for each clip. Give a shorter limit for
  hooks and a longer one for complete explanations.
- **`aspect`** — use `9:16` for Shorts, Reels, and TikTok; switch to `1:1`,
  `4:5`, or `16:9` when the destination needs it.

The recipe options contain the caption style, language, corrections, and
parallelism controls.

## Notes

A strong source does not guarantee that every requested count exists. The agent
may return fewer clips rather than repeat the same idea or cut a weak moment.
Review each selection in context before publishing: an isolated quote can lose
important qualifications from the original conversation.

The sample gallery was produced in one resumable run from the credited OpenClaw
interview. It demonstrates the output format; AI selection can choose different
moments on a new run.
