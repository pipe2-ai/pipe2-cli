// regen-cookbook walks the cookbook registry and writes each recipe's
// Manifest as packages/cookbook/<slug>/recipe.json, plus a refreshed
// packages/cookbook/index.json. Run via `make cookbook-regen`.
//
// The Go source is the source of truth; this tool emits the JSON
// projection the website's content loader reads. Idempotent — diff
// against committed JSON to verify drift in CI.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pipe2-ai/pipe2-cli/cookbook"
	_ "github.com/pipe2-ai/pipe2-cli/cookbook/all"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	root = filepath.Join(root, "packages", "pipe2-cli", "cookbook")

	type indexEntry struct {
		Slug          string   `json:"slug"`
		Title         string   `json:"title"`
		Description   string   `json:"description"`
		Category      string   `json:"category"`
		Tags          []string `json:"tags"`
		SchemaVersion int      `json:"schema_version"`
		ChainLength   int      `json:"chain_length"`
		UpdatedAt     string   `json:"updatedAt"`
	}

	// Disk-discover recipes that exist in the cookbook tree but
	// aren't yet ported to Go. Their existing recipe.json stays
	// untouched (we only write the Go-registered ones below);
	// they still appear in index.json so the web keeps rendering.
	// Keyed by directory name (snake_case) so the disk-discovery loop
	// below — which compares against d.Name() — correctly skips Go
	// recipes. Keying by the kebab-case slug silently never matched,
	// so every ported recipe also got a duplicate "legacy" entry.
	registered := map[string]bool{}
	for _, r := range cookbook.All() {
		registered[pkgDirName(r.Manifest().Slug)] = true
	}
	var legacy []indexEntry
	if dirs, _ := os.ReadDir(root); len(dirs) > 0 {
		for _, d := range dirs {
			if !d.IsDir() || d.Name()[0] == '_' || registered[d.Name()] {
				continue
			}
			rj := filepath.Join(root, d.Name(), "recipe.json")
			b, err := os.ReadFile(rj)
			if err != nil {
				continue
			}
			var m struct {
				Slug, Title, Description, Category, UpdatedAt string
				Tags                                          []string
				SchemaVersion                                 int        `json:"schema_version"`
				Chain                                         []struct{} `json:"chain"`
			}
			if err := json.Unmarshal(b, &m); err != nil {
				continue
			}
			legacy = append(legacy, indexEntry{
				Slug: m.Slug, Title: m.Title, Description: m.Description,
				Category: m.Category, Tags: m.Tags,
				SchemaVersion: m.SchemaVersion, ChainLength: len(m.Chain),
				UpdatedAt: m.UpdatedAt,
			})
		}
	}

	all := cookbook.All()
	entries := make([]indexEntry, 0, len(all)+len(legacy))
	entries = append(entries, legacy...)
	for _, r := range all {
		m := r.Manifest()
		m.FillSchemaVersion()
		// Recipes live in the Go package directory (snake_case),
		// alongside recipe.go + README.md. The slug stays kebab-case
		// for URLs; the web loader maps dir → slug via recipe.json.
		dirName := pkgDirName(m.Slug)
		dir := filepath.Join(root, dirName)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			die("mkdir %s: %v", dir, err)
		}
		out := filepath.Join(dir, "recipe.json")
		raw, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			die("marshal %s: %v", m.Slug, err)
		}
		if err := os.WriteFile(out, append(raw, '\n'), 0o644); err != nil {
			die("write %s: %v", out, err)
		}
		fmt.Printf("✓ %s\n", out)

		entries = append(entries, indexEntry{
			Slug: m.Slug, Title: m.Title, Description: m.Description,
			Category: m.Category, Tags: m.Tags,
			SchemaVersion: m.SchemaVersion, ChainLength: len(m.Chain),
			UpdatedAt: m.UpdatedAt,
		})
	}

	idx := map[string]any{
		"schema_version": cookbook.SchemaVersion,
		"generated_at":   time.Now().UTC().Format(time.RFC3339),
		"recipes":        entries,
	}
	idxPath := filepath.Join(root, "index.json")
	idxRaw, _ := json.MarshalIndent(idx, "", "  ")
	if err := os.WriteFile(idxPath, append(idxRaw, '\n'), 0o644); err != nil {
		die("write %s: %v", idxPath, err)
	}
	fmt.Printf("✓ %s — %d recipe%s\n", idxPath, len(entries), pluralS(len(entries)))
}

func die(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...); os.Exit(1) }
func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// pkgDirName converts a kebab-case slug to a snake_case directory
// name matching the Go package convention (Go forbids hyphens in
// package names). e.g. "clip-factory" → "clip_factory".
func pkgDirName(slug string) string {
	out := make([]byte, len(slug))
	for i := 0; i < len(slug); i++ {
		if slug[i] == '-' {
			out[i] = '_'
		} else {
			out[i] = slug[i]
		}
	}
	return string(out)
}
