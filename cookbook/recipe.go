package cookbook

import (
	"fmt"
	"sort"
	"sync"
)

// Recipe is the contract every cookbook entry implements. A recipe
// is a Go program registered at init() time; the CLI looks it up by
// slug and invokes Run with a Context built from validated user
// inputs and a live Pipe2 client.
type Recipe interface {
	Manifest() Manifest
	Run(ctx *Context) error
}

// registry holds every Register'd recipe by slug. Reads are concurrent
// (the CLI may call Lookup from multiple goroutines during list/info)
// so the map is read-mostly under sync.RWMutex.
var (
	registryMu sync.RWMutex
	registry   = map[string]Recipe{}
)

// Register adds a recipe to the global registry. Call from init():
//
//	func init() { cookbook.Register(&Recipe{}) }
//
// Panics if the same slug registers twice — recipes are compiled in
// at build time so a duplicate is a programmer error, not a runtime
// condition.
func Register(r Recipe) {
	m := r.Manifest()
	if m.Slug == "" {
		panic("cookbook.Register: recipe has empty Manifest().Slug")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[m.Slug]; exists {
		panic(fmt.Sprintf("cookbook.Register: duplicate slug %q", m.Slug))
	}
	registry[m.Slug] = r
}

// Lookup returns the recipe with the given slug, or false if no
// recipe is registered under that name. Used by `pipe2 recipe info`
// and `pipe2 recipe run` to resolve user input.
func Lookup(slug string) (Recipe, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	r, ok := registry[slug]
	return r, ok
}

// All returns every registered recipe sorted by slug. Used by
// `pipe2 recipe list` and the manifest generator. Sorted output
// makes the generator's diffs deterministic across machines.
func All() []Recipe {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]Recipe, 0, len(registry))
	for _, r := range registry {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Manifest().Slug < out[j].Manifest().Slug
	})
	return out
}

// Reset wipes the registry. Test-only — production code never calls
// this. Exported (rather than test-only) so package-level tests in
// recipe sub-packages can run in isolation.
func Reset() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = map[string]Recipe{}
}
