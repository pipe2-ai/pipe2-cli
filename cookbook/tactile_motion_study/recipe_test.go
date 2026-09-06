package tactile_motion_study_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/pipe2-ai/pipe2-cli/cookbook"
	"github.com/pipe2-ai/pipe2-cli/cookbook/tactile_motion_study"
)

type call struct {
	slug   string
	inputs map[string]any
}

type mockClient struct {
	t       *testing.T
	calls   []call
	outputs map[string]map[string]any
}

func (m *mockClient) RunPipeline(_ context.Context, slug string, input json.RawMessage) (string, error) {
	var parsed map[string]any
	if err := json.Unmarshal(input, &parsed); err != nil {
		m.t.Fatalf("RunPipeline(%s): bad input JSON: %v", slug, err)
	}
	m.calls = append(m.calls, call{slug: slug, inputs: parsed})
	return slug, nil
}

func (m *mockClient) WaitRun(_ context.Context, runID string, _ time.Duration) (*cookbook.RunRow, error) {
	return &cookbook.RunRow{ID: runID, Status: "completed", Output: m.outputs[runID]}, nil
}

func (m *mockClient) UploadAsset(_ context.Context, localPath string) (string, error) {
	return "https://uploaded.test/" + localPath, nil
}

func (m *mockClient) EstimatePipelineCost(_ context.Context, _ string, _ json.RawMessage) (*cookbook.PipelineCostEstimate, error) {
	return nil, nil
}

func TestRunOrchestratesSilentMotionAndExactTitle(t *testing.T) {
	mc := &mockClient{
		t: t,
		outputs: map[string]map[string]any{
			"image-generator": {"image_url": "https://cdn.test/keyframe.png"},
			"video-generator": {"video_url": "https://cdn.test/motion.mp4"},
			"text-card": {
				"video_url": "https://cdn.test/final.mp4",
				"meta":      map[string]any{"drawtext_params": `{"text":"SOFT MATTER STUDY"}`},
			},
		},
	}
	r := &tactile_motion_study.Recipe{}
	inputs, err := cookbook.ResolveInputs(r.Manifest(), map[string]string{})
	if err != nil {
		t.Fatalf("resolve defaults: %v", err)
	}
	ctx := cookbook.NewContext(context.Background(), mc, inputs)
	if err := r.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := ctx.FinalOutput(); got != "https://cdn.test/final.mp4" {
		t.Fatalf("final output = %q", got)
	}
	if len(mc.calls) != 3 {
		t.Fatalf("calls = %d, want 3", len(mc.calls))
	}
	for i, want := range []string{"image-generator", "video-generator", "text-card"} {
		if got := mc.calls[i].slug; got != want {
			t.Errorf("call %d = %q, want %q", i, got, want)
		}
	}
	image := mc.calls[0].inputs
	if image["model"] != "gemini-3-1-flash-image" || image["aspect_ratio"] != "9:16" || image["enhance_prompt"] != false {
		t.Errorf("image inputs = %#v", image)
	}
	if prompt, _ := image["prompt"].(string); !strings.Contains(prompt, "pearlescent foam pebble") || !strings.Contains(prompt, "No words") {
		t.Errorf("image prompt does not preserve the text-free material brief: %q", prompt)
	}
	motion := mc.calls[1].inputs
	if motion["model"] != "minimax-h3" || motion["start_image"] != "https://cdn.test/keyframe.png" || motion["duration"] != "5" || motion["resolution"] != "768p" || motion["aspect_ratio"] != "9:16" {
		t.Errorf("motion inputs = %#v", motion)
	}
	if audio, ok := motion["audio"].(bool); !ok || audio {
		t.Errorf("motion audio = %#v, want false", motion["audio"])
	}
	card := mc.calls[2].inputs
	if card["text"] != "SOFT MATTER STUDY" || card["background_url"] != "https://cdn.test/motion.mp4" {
		t.Errorf("text-card inputs = %#v", card)
	}

	mc.outputs["text-card"] = map[string]any{
		"video_url": "https://cdn.test/changed.mp4",
		"meta":      map[string]any{"drawtext_params": `{"text":"Soft Matter Study"}`},
	}
	badCtx := cookbook.NewContext(context.Background(), mc, inputs)
	if err := r.Run(badCtx); err == nil || !strings.Contains(err.Error(), "changed the exact title") {
		t.Fatalf("changed title error = %v", err)
	}
}
