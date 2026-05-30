# Writing a recipe article

A recipe page is assembled from two sources. Keep them in their lanes
and the article stays tight. Blur them and the reader reads the same
thing three times before reaching anything new.

## Two sources, no overlap

**`recipe.json`** — generated from `recipe.go`, never hand-edited — is
structured data. The article page renders it as:

| Field | Renders as |
|---|---|
| `description` | hero one-liner + SEO meta |
| `intro_voiceover` | hero hook paragraph (the *why*) |
| `chain[].what_it_does` | the step-by-step Walkthrough |
| `inputs` | the Run It panel + manifest |
| `example_command`, `agent_prompt` | the Run It tabs |
| `samples` | the sample previews |

**`README.md`** is the narrative the data can't carry: *why* the recipe
is built the way it is, *which* knobs actually change the output, and
*what* will bite you. It is not a place to restate the data above.

### Each fact lives in exactly one place

| Fact | Owned by | README must NOT |
|---|---|---|
| What it produces (one line) | `description` | re-open the body with it |
| Why the naive approach fails | `intro_voiceover` | repeat the hook |
| What each step does | `chain[].what_it_does` → Walkthrough | re-list the chain in prose |
| Every input + default | `inputs` → Run It / manifest | dump the full flag list |
| Credits / cost | computed live in Run It | quote a number |

The chain rule is not negotiable. Hand-written step lists drift —
dance-reel's README once listed the music step as #5 while the recipe
ran it as #2. The Walkthrough is the single source of truth for steps;
the README never enumerates them.

## The README skeleton

Three sections, fixed headings, this order. ~200–350 words total.

```markdown
## Why it works

[1–2 paragraphs on the *insight* — the non-obvious idea that makes this
recipe better than a single model call. Explain the mechanism, not the
steps. Assume the reader already got the problem from the hero hook;
start from the solution.]

## The knobs that matter

- **`flag`** — the 2–4 inputs that genuinely change the output, each
  with a one-line *why*. Not all of them.
[Close with one line pointing at the manifest for the full set.]

## Notes

[Optional. Recipe-specific gotchas only — a real failure mode, named,
with the fix. Skip the section entirely if there's nothing real to
warn about.]
```

If every input is under "knobs that matter," the heading is a lie —
cut it to the few that move the needle.

## Voice

- Lead from the mechanism, concretely. "The grid is a storyboard" beats
  "the recipe improves output quality."
- Specific over generic, always. "moonwalk, body roll" not "dance moves."
- Every model or tool choice earns a one-line *why*, or it doesn't
  appear at all.
- Second person, present tense, active voice. One idea per sentence.
- No ops trivia in the narrative. Retry, soft-fail, quota, and cache
  behavior are reference material — keep them out unless they change
  what the reader should *do*.
- Inline `code` for every flag, pipeline slug, and file name.

## Don't

- Don't restate `description` or `intro_voiceover` in the body.
- Don't enumerate the chain — the Walkthrough already does, accurately.
- Don't embed sample `<video>`/`<img>` tags — the Walkthrough and the
  sample card render previews from `samples`.
- Don't list every input — highlight the few that matter.
- Don't quote credit costs — the Run It panel computes them live.
