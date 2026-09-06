package product_world_tour_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/pipe2-ai/pipe2-cli/cookbook"
	"github.com/pipe2-ai/pipe2-cli/cookbook/product_world_tour"
)

type client struct {
	slugs  []string
	inputs []map[string]any
	failAt int
}

func (m *client) RunPipeline(_ context.Context, slug string, raw json.RawMessage) (string, error) {
	if len(m.slugs)+1 == m.failAt {
		return "", fmt.Errorf("failed stage")
	}
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", err
	}
	m.slugs = append(m.slugs, slug)
	m.inputs = append(m.inputs, input)
	return fmt.Sprint(len(m.slugs)), nil
}
func (*client) WaitRun(_ context.Context, id string, _ time.Duration) (*cookbook.RunRow, error) {
	key, ext := "image_url", ".png"
	if id == "4" {
		key, ext = "video_url", ".mp4"
	}
	return &cookbook.RunRow{ID: id, Status: "completed", Output: map[string]any{key: "https://cdn.test/" + id + ext}}, nil
}
func (*client) UploadAsset(context.Context, string) (string, error) {
	return "", fmt.Errorf("unexpected upload")
}
func (*client) EstimatePipelineCost(context.Context, string, json.RawMessage) (*cookbook.PipelineCostEstimate, error) {
	return nil, fmt.Errorf("unexpected estimate")
}

func TestReferenceHandoffsAndStopOnFailedEdit(t *testing.T) {
	r := &product_world_tour.Recipe{}
	inputs, err := cookbook.ResolveInputs(r.Manifest(), map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	m := &client{}
	ctx := cookbook.NewContext(context.Background(), m, inputs)
	if err := r.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m.slugs, []string{"image-generator", "image-editor", "image-editor", "video-generator"}) {
		t.Fatal(m.slugs)
	}
	for _, i := range []int{1, 2} {
		if !reflect.DeepEqual(m.inputs[i]["image_urls"], []any{"https://cdn.test/1.png"}) {
			t.Fatal("edits must use original product", m.inputs[i])
		}
	}
	v := m.inputs[3]
	if !reflect.DeepEqual(v["reference_images"], []any{"https://cdn.test/1.png", "https://cdn.test/2.png", "https://cdn.test/3.png"}) {
		t.Fatal(v)
	}
	for k, want := range r.Manifest().Chain[3].With {
		if v[k] != want {
			t.Errorf("%s = %v, want %v", k, v[k], want)
		}
	}
	if ctx.FinalOutput() != "https://cdn.test/4.mp4" {
		t.Fatal(ctx.FinalOutput())
	}
	m = &client{failAt: 3}
	ctx = cookbook.NewContext(context.Background(), m, inputs)
	if err := r.Run(ctx); err == nil || len(m.slugs) != 2 || ctx.FinalOutput() != "" {
		t.Fatal("failed edit continued")
	}
}
