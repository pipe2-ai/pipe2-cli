package cookbook

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// The Pipe2 brand mark is packaged into the CLI binary at build time so
// recipes can apply it without a network round-trip. Every variant is
// the same logo lockup (infinity glyph + the Space Grotesk wordmark) in
// a different colour — see scripts/render-watermarks.sh, which renders them
// all from packages/web/public/pipe2-watermark.svg.
//
// "light" is the minimal mark — a white wordmark, for dark footage only.
// Every other variant is built in the "universal" style (coloured
// wordmark + white outline), so it stays legible on bright AND dark
// footage; "dark" is simply the purple member of that family. The
// remaining colours let a caller match the mark to a clip's palette.

//go:embed assets/watermark-light.png
var watermarkLightPNG []byte

//go:embed assets/watermark-dark.png
var watermarkDarkPNG []byte

//go:embed assets/watermark-aqua.png
var watermarkAquaPNG []byte

//go:embed assets/watermark-crimson.png
var watermarkCrimsonPNG []byte

//go:embed assets/watermark-amber.png
var watermarkAmberPNG []byte

//go:embed assets/watermark-emerald.png
var watermarkEmeraldPNG []byte

//go:embed assets/watermark-indigo.png
var watermarkIndigoPNG []byte

// watermarkVariants maps a variant name to its embedded PNG. Keep this
// in sync with the COLOURS table in scripts/render-watermarks.sh.
var watermarkVariants = map[string][]byte{
	"light":   watermarkLightPNG,
	"dark":    watermarkDarkPNG,
	"aqua":    watermarkAquaPNG,
	"crimson": watermarkCrimsonPNG,
	"amber":   watermarkAmberPNG,
	"emerald": watermarkEmeraldPNG,
	"indigo":  watermarkIndigoPNG,
}

var (
	watermarkMu    sync.Mutex
	watermarkPaths = map[string]string{}
)

// WatermarkVariants returns the supported variant names, sorted — for
// recipe Input enum declarations so they cannot drift from the embeds.
func WatermarkVariants() []string {
	names := make([]string, 0, len(watermarkVariants))
	for name := range watermarkVariants {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// WatermarkInputs is the standard four-knob input set every recipe
// declares to brand its output: --watermark-url, --no-watermark,
// --watermark-variant, --watermark-scale. Spread the return value into
// your Manifest().Inputs so the CLI flags and the form-rendered schema
// stay identical across recipes.
func WatermarkInputs() []Input {
	return []Input{
		{Name: "watermark_url", Type: AssetURL, Default: "", CLIArg: "--watermark-url",
			Description: "Override the default Pipe2 logo with your own image (URL or local path). Empty means use the bundled Pipe2 watermark."},
		{Name: "no_watermark", Type: Bool, Default: false, CLIArg: "--no-watermark",
			Description: "Ship the output unbranded. Skips the watermark step entirely."},
		{Name: "watermark_variant", Type: Enum, Default: "light", CLIArg: "--watermark-variant",
			Values:      WatermarkVariants(),
			Description: "Which bundled Pipe2 logo to use when --watermark-url is empty. \"light\" for dark videos, \"dark\" for bright ones; coloured variants match the clip palette."},
		{Name: "watermark_scale_pct", Type: Int, Default: int64(20), CLIArg: "--watermark-scale",
			Description: "Watermark width as a percentage of the video width."},
	}
}

// WatermarkChainStep is the canonical chain-step descriptor for the
// watermark pipeline. Append it to Manifest().Chain as the last step
// of any video-producing recipe so /recipes pages render it with a
// consistent description, cost label, and ordering.
func WatermarkChainStep() ChainStep {
	return ChainStep{
		Pipeline:     "watermark",
		ArtifactKind: Video,
		WhatItDoes:   "Overlays the Pipe2 logo (or your own --watermark-url) at the top-left corner so the clip carries attribution across reposts. Pass --no-watermark to skip.",
	}
}

// ResolveWatermark turns the standard watermark inputs into a single
// uploaded asset URL ready for the watermark pipeline. Returns "" when
// --no-watermark is set so the caller can branch with a plain
// `if watermarkURL != ""`. When the user didn't supply --watermark-url,
// the bundled brand asset for --watermark-variant is materialized and
// the resulting path is upload-resolved.
//
// Call this ONCE per recipe run, before any parallel work, so every
// per-clip goroutine reuses the same uploaded URL rather than racing
// to upload the same logo N times.
func ResolveWatermark(ctx *Context) (string, error) {
	if ctx.Inputs.Bool("no_watermark") {
		return "", nil
	}
	url := ctx.ResolveAssetURL("watermark_url")
	if url != "" {
		return url, nil
	}
	path, err := MaterializeDefaultWatermark(ctx.Inputs.String("watermark_variant"))
	if err != nil {
		return "", fmt.Errorf("default watermark: %w", err)
	}
	ctx.Inputs["watermark_url"] = path
	return ctx.ResolveAssetURL("watermark_url"), nil
}

// ApplyWatermark dispatches the watermark pipeline against sourceURL
// with the canonical top-left, 0.85-opacity, 4%-margin overlay every
// recipe uses. When watermarkURL is "" (--no-watermark) the function
// is a no-op and returns sourceURL unchanged, so callers can wrap a
// watermark pass around any final URL without branching.
//
// scalePct is taken as a separate argument because recipes pull it
// from their own Inputs (watermark_scale_pct) and pass it through —
// keeping the helper free of input-map lookups so it composes cleanly
// from parallel goroutines that share the resolved URL via closure.
func ApplyWatermark(sub *Context, sourceURL, watermarkURL string, scalePct int64) (string, error) {
	if watermarkURL == "" {
		return sourceURL, nil
	}
	wm, err := sub.RunPipeline("watermark", Inputs{
		"source_video_url": sourceURL,
		"watermark_url":    watermarkURL,
		"position":         "top_left",
		"opacity":          0.85,
		"scale_pct":        scalePct,
		"margin_pct":       4,
	})
	if err != nil {
		return "", err
	}
	return wm.URL("video_url"), nil
}

// MaterializeDefaultWatermark writes the embedded brand mark for the
// given variant to a process-lifetime temp file and returns its path —
// recipes feed that path into ResolveAssetURL, which auto-uploads it so
// the worker can pull it down. An unknown variant falls back to "light".
// Materialised once per variant; the temp file is reaped at process exit.
func MaterializeDefaultWatermark(variant string) (string, error) {
	data, ok := watermarkVariants[variant]
	if !ok {
		variant, data = "light", watermarkLightPNG
	}

	watermarkMu.Lock()
	defer watermarkMu.Unlock()
	if p, ok := watermarkPaths[variant]; ok {
		return p, nil
	}
	dir, err := os.MkdirTemp("", "pipe2-watermark-*")
	if err != nil {
		return "", fmt.Errorf("watermark: mkdir temp: %w", err)
	}
	path := filepath.Join(dir, "pipe2-watermark-"+variant+".png")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("watermark: write embedded asset: %w", err)
	}
	watermarkPaths[variant] = path
	return path, nil
}
