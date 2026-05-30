package dance_reel_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/pipe2-ai/pipe2-cli/cookbook"
	"github.com/pipe2-ai/pipe2-cli/cookbook/dance_reel"
)

type mockClient struct {
	t        *testing.T
	calls    []call
	outputs  map[string]map[string]any
	uploaded []string
}

type call struct {
	slug   string
	inputs map[string]any
}

func (m *mockClient) RunPipeline(_ context.Context, slug string, input json.RawMessage) (string, error) {
	var parsed map[string]any
	if err := json.Unmarshal(input, &parsed); err != nil {
		m.t.Fatalf("RunPipeline(%s): bad input json: %v", slug, err)
	}
	m.calls = append(m.calls, call{slug: slug, inputs: parsed})
	return "run-" + slug + "-" + intToStr(len(m.calls)), nil
}

func (m *mockClient) WaitRun(_ context.Context, runID string, _ time.Duration) (*cookbook.RunRow, error) {
	// runID is "run-<slug>-<n>". Strip the trailing -<n> to look up.
	slug := runID[len("run-"):]
	for i := len(slug) - 1; i >= 0; i-- {
		if slug[i] == '-' {
			slug = slug[:i]
			break
		}
	}
	out, ok := m.outputs[slug]
	if !ok {
		m.t.Fatalf("WaitRun: no canned output for %q", slug)
	}
	return &cookbook.RunRow{ID: runID, Status: "completed", Output: out}, nil
}

func (m *mockClient) UploadAsset(_ context.Context, localPath string) (string, error) {
	m.uploaded = append(m.uploaded, localPath)
	return "https://uploaded.test/" + localPath, nil
}

func (m *mockClient) EstimatePipelineCost(_ context.Context, _ string, _ json.RawMessage) (*cookbook.PipelineCostEstimate, error) {
	return nil, nil
}

func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func TestRun_HappyPath(t *testing.T) {
	mc := &mockClient{
		t: t,
		outputs: map[string]map[string]any{
			"image-generator":  {"image_url": "https://cdn.test/grid.png"},
			"image-motion":     {"video_url": "https://cdn.test/reveal.mp4"},
			"video-generator":  {"video_url": "https://cdn.test/dance.mp4"},
			"music-generator":  {"audio_url": "https://cdn.test/music.mp3"},
			"video-reel":       {"video_url": "https://cdn.test/reel.mp4"},
			"watermark":        {"video_url": "https://cdn.test/final-branded.mp4"},
		},
	}
	r := &dance_reel.Recipe{}

	inputs, err := cookbook.ResolveInputs(r.Manifest(), map[string]string{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	ctx := cookbook.NewContext(context.Background(), mc, inputs)
	if err := r.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Watermark is on by default — embedded brand asset materializes,
	// uploads via the mock, and the final URL is the branded reel.
	if got := ctx.FinalOutput(); got != "https://cdn.test/final-branded.mp4" {
		t.Errorf("final output = %q, want final-branded.mp4", got)
	}

	// 6 calls: grid → music → reveal → dance → reel → watermark.
	if len(mc.calls) != 6 {
		t.Fatalf("calls = %d, want 6", len(mc.calls))
	}
	want := []string{"image-generator", "music-generator", "image-motion", "video-generator", "video-reel", "watermark"}
	for i, w := range want {
		if mc.calls[i].slug != w {
			t.Errorf("calls[%d].slug = %q, want %q", i, mc.calls[i].slug, w)
		}
	}
	// Music pinned to eleven-music-v1 with vocals=true.
	if got := mc.calls[1].inputs["model"]; got != "eleven-music-v1" {
		t.Errorf("music model = %v, want eleven-music-v1", got)
	}
	if got := mc.calls[1].inputs["vocals"]; got != true {
		t.Errorf("music vocals = %v, want true", got)
	}

	// GPT Image 2 pinned for the grid step.
	if got := mc.calls[0].inputs["model"]; got != "gpt-image-2" {
		t.Errorf("grid model = %v, want gpt-image-2", got)
	}

	// Seedance Pro pinned, conditioned on the music as reference_audio.
	if got := mc.calls[3].inputs["model"]; got != "seedance-2-0-pro" {
		t.Errorf("dance model = %v, want seedance-2-0-pro", got)
	}
	refs, _ := mc.calls[3].inputs["reference_images"].([]any)
	if len(refs) != 1 || refs[0] != "https://cdn.test/grid.png" {
		t.Errorf("dance reference_images = %v, want [grid.png]", refs)
	}
	if got := mc.calls[3].inputs["audio"]; got != false {
		t.Errorf("dance audio = %v, want false", got)
	}
	if got := mc.calls[3].inputs["reference_audio_url"]; got != "https://cdn.test/music.mp3" {
		t.Errorf("dance reference_audio_url = %v, want music.mp3 (audio-conditioned path)", got)
	}

	// Reel: 2 video URLs.
	videos, _ := mc.calls[4].inputs["video_urls"].([]any)
	if len(videos) != 2 || videos[0] != "https://cdn.test/reveal.mp4" || videos[1] != "https://cdn.test/dance.mp4" {
		t.Errorf("reel video_urls = %v, want [reveal, dance]", videos)
	}
}

func TestManifest_Shape(t *testing.T) {
	m := (&dance_reel.Recipe{}).Manifest()
	if m.Slug != "dance-reel" {
		t.Errorf("Slug = %q", m.Slug)
	}
	if len(m.Chain) != 6 {
		t.Errorf("Chain length = %d, want 6", len(m.Chain))
	}
	// Audio-conditioned order: grid (img) → music (audio) → reveal (vid)
	// → dance (vid) → reel (vid) → watermark (vid).
	wantArtifacts := []cookbook.ArtifactKind{
		cookbook.Image, cookbook.Audio, cookbook.Video,
		cookbook.Video, cookbook.Video, cookbook.Video,
	}
	for i, kind := range wantArtifacts {
		if m.Chain[i].ArtifactKind != kind {
			t.Errorf("Chain[%d].ArtifactKind = %q, want %q", i, m.Chain[i].ArtifactKind, kind)
		}
	}
}

// TestRun_NoWatermark verifies the --no-watermark opt-out skips the
// watermark step entirely. The final URL is the un-watermarked reel.
func TestRun_NoWatermark(t *testing.T) {
	mc := &mockClient{
		t: t,
		outputs: map[string]map[string]any{
			"image-generator": {"image_url": "https://cdn.test/grid.png"},
			"image-motion":    {"video_url": "https://cdn.test/reveal.mp4"},
			"video-generator": {"video_url": "https://cdn.test/dance.mp4"},
			"music-generator": {"audio_url": "https://cdn.test/music.mp3"},
			"video-reel":      {"video_url": "https://cdn.test/final.mp4"},
		},
	}
	r := &dance_reel.Recipe{}

	inputs, err := cookbook.ResolveInputs(r.Manifest(), map[string]string{
		"--no-watermark": "true",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	ctx := cookbook.NewContext(context.Background(), mc, inputs)
	if err := r.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(mc.calls) != 5 {
		t.Fatalf("calls = %d, want 5 (no watermark)", len(mc.calls))
	}
	if got := ctx.FinalOutput(); got != "https://cdn.test/final.mp4" {
		t.Errorf("final output = %q, want final.mp4 (no branding)", got)
	}
}

// TestRun_CustomWatermark verifies --watermark-url overrides the
// embedded default brand asset.
func TestRun_CustomWatermark(t *testing.T) {
	mc := &mockClient{
		t: t,
		outputs: map[string]map[string]any{
			"image-generator": {"image_url": "https://cdn.test/grid.png"},
			"image-motion":    {"video_url": "https://cdn.test/reveal.mp4"},
			"video-generator": {"video_url": "https://cdn.test/dance.mp4"},
			"music-generator": {"audio_url": "https://cdn.test/music.mp3"},
			"video-reel":      {"video_url": "https://cdn.test/reel.mp4"},
			"watermark":       {"video_url": "https://cdn.test/final-branded.mp4"},
		},
	}
	r := &dance_reel.Recipe{}

	inputs, err := cookbook.ResolveInputs(r.Manifest(), map[string]string{
		"--watermark-url": "https://example.com/logo.png",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	ctx := cookbook.NewContext(context.Background(), mc, inputs)
	if err := r.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(mc.calls) != 6 {
		t.Fatalf("calls = %d, want 6", len(mc.calls))
	}
	if got := mc.calls[5].inputs["watermark_url"]; got != "https://example.com/logo.png" {
		t.Errorf("watermark url = %v, want logo.png (custom override)", got)
	}
	if got := ctx.FinalOutput(); got != "https://cdn.test/final-branded.mp4" {
		t.Errorf("final output = %q, want final-branded.mp4", got)
	}
}
