# pipe2-cli developer tasks.
#
# The cookbook recipe.json / index.json files and AGENT.md are
# generated projections of the Go source — never hand-edit them. Run
# the matching `*-regen` target after changing the source, and the
# `*-check` targets gate drift in CI (see .github/workflows/ci.yml).

.PHONY: cookbook-regen cookbook-check agent-md agent-md-check build vet test check

# Regenerate the cookbook JSON projection (each cookbook/<slug>/recipe.json
# plus cookbook/index.json) from the Go Manifest() source of truth.
cookbook-regen:
	go run ./cmd/regen-cookbook

# Drift gate: regenerate, then fail if the committed cookbook JSON is
# out of sync with the Go source. index.json is deterministic (its
# generated_at is pinned to the latest recipe updatedAt, not wall-clock
# time), so this only fires on a real source change.
cookbook-check: cookbook-regen
	git diff --exit-code -- cookbook

# Regenerate AGENT.md from the embedded SKILL.md prose + the live cobra
# command tree.
agent-md:
	go run ./cmd/gen-agent-md

# Drift gate for AGENT.md.
agent-md-check: agent-md
	git diff --exit-code -- AGENT.md

build:
	go build ./...

vet:
	go vet ./...

test:
	go test ./...

# Everything the CI build job enforces, in one shot.
check: build vet test cookbook-check agent-md-check
