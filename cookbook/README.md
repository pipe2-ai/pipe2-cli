# Pipe2.ai cookbook

Canonical, runnable recipes for every tutorial published at
[pipe2.ai/learn](https://pipe2.ai/learn). Each recipe is the **single
source of truth** for its tutorial: the article over there imports
this directory's `recipe.sh` at build time, so editing one updates
both.

## Why this exists

We learned the hard way that prose-only tutorials drift away from the
live API. The `*_asset_url` → `*_asset_id` rename lived in the wild
for months in our published guides because nothing forced the prose
and the schema to agree. With recipes as real files in the repo,
schema changes can fail CI on the recipe and update both at once in
the same PR.

## Layout

Each recipe lives at `cookbook/<slug>/`:

| File | Audience |
|---|---|
| `README.md` | Humans cloning the repo or browsing on GitHub |
| `recipe.sh` | The canonical runnable. End-to-end bash, idempotent, env-var-driven |
| `recipe.json` | Agent-readable manifest — chain shape, inputs, outputs, cost |
| `inputs/` | Optional fixtures (small public-domain clips, sample prompts) |

## How recipes are consumed

- **Humans**: `git clone`, `cd <slug>`, follow the recipe's README.
- **AI agents**: parse `recipe.json` for chain semantics, fall back to
  `recipe.sh` for the executable form. Both files are stable contracts.
- **Published tutorials at [pipe2.ai/learn](https://pipe2.ai/learn)**: import
  `recipe.sh` directly so the article and the runnable can never drift.

## Recipes

| Slug | Tutorial |
|---|---|
| [`clip-factory`](./clip_factory/) | Long-form video → N captioned shorts |
| [`dance-reel`](./dance_reel/) | Subject → AI dance reel with captions |

## Adding a new recipe

1. Create `<slug>/` with `README.md`, `recipe.sh`, `recipe.json`.
2. Run the recipe end-to-end against the live API at least once.
3. Update the index above.

Recipes are self-contained: they never reference paths outside this
directory. That keeps the cookbook portable and publishable on its own.
