package hidden_world_reveal_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pipe2-ai/pipe2-cli/cookbook"
	"github.com/pipe2-ai/pipe2-cli/cookbook/hidden_world_reveal"
)

type mockClient struct {
	inputs []map[string]any
	slugs  []string
	failAt string
}

func (m *mockClient) RunPipeline(_ context.Context, slug string, raw json.RawMessage) (string, error) {
	if slug == m.failAt {
		return "", fmt.Errorf("failed %s", slug)
	}
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", err
	}
	m.slugs = append(m.slugs, slug)
	m.inputs = append(m.inputs, input)
	return slug, nil
}

func (m *mockClient) WaitRun(_ context.Context, id string, _ time.Duration) (*cookbook.RunRow, error) {
	key, ext := "image_url", ".png"
	if id == "video-generator" {
		key, ext = "video_url", ".mp4"
	}
	return &cookbook.RunRow{ID: id, Status: "completed", Output: map[string]any{key: "https://cdn.test/" + id + ext}}, nil
}

func (m *mockClient) UploadAsset(_ context.Context, _ string) (string, error) {
	return "", fmt.Errorf("unexpected upload")
}

func (m *mockClient) EstimatePipelineCost(_ context.Context, _ string, _ json.RawMessage) (*cookbook.PipelineCostEstimate, error) {
	return nil, fmt.Errorf("unexpected estimate")
}

func TestRunBuildsWorldBeforeDerivingClosedOpening(t *testing.T) {
	r := &hidden_world_reveal.Recipe{}
	inputs, err := cookbook.ResolveInputs(r.Manifest(), map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	m := &mockClient{}
	ctx := cookbook.NewContext(context.Background(), m, inputs)
	if err := r.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Join(m.slugs, ",") != "image-generator,image-editor,video-generator" {
		t.Fatalf("stage order = %v", m.slugs)
	}
	if m.inputs[0]["model"] != "gpt-image-2" || m.inputs[0]["quality"] != "auto" || m.inputs[1]["model"] != "gemini-3-1-flash-image" {
		t.Fatalf("reference models differ from the reviewed recipe: %v", m.inputs[:2])
	}
	editSources, _ := m.inputs[1]["image_urls"].([]any)
	if len(editSources) != 1 || editSources[0] != "https://cdn.test/image-generator.png" {
		t.Fatalf("closing edit lost its open-world reference: %v", editSources)
	}
	video := m.inputs[2]
	for key, want := range map[string]any{
		"start_image": "https://cdn.test/image-editor.png", "end_image": "https://cdn.test/image-generator.png",
		"model": "minimax-h3", "duration": "8", "resolution": "768p", "aspect_ratio": "9:16", "audio": false,
	} {
		if video[key] != want {
			t.Errorf("video %s = %v, want %v", key, video[key], want)
		}
	}
	if ctx.FinalOutput() != "https://cdn.test/video-generator.mp4" {
		t.Fatalf("final output = %q", ctx.FinalOutput())
	}

	// A failed closing edit must stop the recipe before a paid video is started.
	m = &mockClient{failAt: "image-editor"}
	ctx = cookbook.NewContext(context.Background(), m, inputs)
	if err := r.Run(ctx); err == nil || len(m.slugs) != 1 || ctx.FinalOutput() != "" {
		t.Fatalf("failed payoff continued: err=%v stages=%v output=%q", err, m.slugs, ctx.FinalOutput())
	}
}
