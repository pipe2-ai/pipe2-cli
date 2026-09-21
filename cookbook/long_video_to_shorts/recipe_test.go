package long_video_to_shorts_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/pipe2-ai/pipe2-cli/cookbook"
	"github.com/pipe2-ai/pipe2-cli/cookbook/long_video_to_shorts"
)

type call struct {
	slug   string
	inputs map[string]any
}

type client struct {
	mu    sync.Mutex
	calls []call
}

func (c *client) RunPipeline(_ context.Context, slug string, input json.RawMessage) (string, error) {
	var parsed map[string]any
	if err := json.Unmarshal(input, &parsed); err != nil {
		return "", err
	}
	c.mu.Lock()
	c.calls = append(c.calls, call{slug: slug, inputs: parsed})
	c.mu.Unlock()
	return slug, nil
}

func (*client) WaitRun(_ context.Context, id string, _ time.Duration) (*cookbook.RunRow, error) {
	output := map[string]any{}
	switch id {
	case "video-trim":
		output["video_urls"] = []any{"/s3/clip-1.mp4", "/s3/clip-2.mp4"}
	case "video-reframe":
		output["video_url"] = "/s3/reframed.mp4"
		output["subject_y_pct"] = 0.4
	case "transcription":
		output["txt_asset_url"] = "/s3/transcript.txt"
		output["srt_asset_url"] = "/s3/transcript.srt"
	case "captions":
		output["video_url"] = "/s3/captioned.mp4"
	}
	return &cookbook.RunRow{ID: id, Status: "completed", Output: output}, nil
}

func (*client) UploadAsset(context.Context, string) (string, error) { return "", nil }
func (*client) EstimatePipelineCost(context.Context, string, json.RawMessage) (*cookbook.PipelineCostEstimate, error) {
	return nil, nil
}

func TestRunUsesAgenticTrimThenProcessesEveryClip(t *testing.T) {
	r := &long_video_to_shorts.Recipe{}
	inputs, err := cookbook.ResolveInputs(r.Manifest(), map[string]string{"--input": "/s3/source.mp4", "--max-clips": "2"})
	if err != nil {
		t.Fatal(err)
	}
	c := &client{}
	ctx := cookbook.NewContext(context.Background(), c, inputs)
	if err = r.Run(ctx); err != nil {
		t.Fatal(err)
	}

	counts := map[string]int{}
	c.mu.Lock()
	for _, call := range c.calls {
		counts[call.slug]++
		if call.slug == "video-trim" {
			if call.inputs["max_clips"] != float64(2) || call.inputs["video_url"] != "/s3/source.mp4" {
				t.Fatalf("incorrect video-trim input: %#v", call.inputs)
			}
		}
		if call.slug == "captions" && call.inputs["transcript_asset_id"] != "/s3/transcript.srt" {
			t.Fatalf("captions did not receive the per-clip transcript: %#v", call.inputs)
		}
	}
	c.mu.Unlock()
	for slug, want := range map[string]int{"video-trim": 1, "video-reframe": 2, "transcription": 3, "captions": 2} {
		if counts[slug] != want {
			t.Fatalf("%s calls = %d, want %d", slug, counts[slug], want)
		}
	}
	var outputs []string
	if err = json.Unmarshal([]byte(ctx.FinalOutput()), &outputs); err != nil || len(outputs) != 2 {
		t.Fatalf("final output = %q, err=%v", ctx.FinalOutput(), err)
	}
}

func TestManifestDeclaresCurrentFlow(t *testing.T) {
	m := (&long_video_to_shorts.Recipe{}).Manifest()
	want := []string{"transcription", "video-trim", "video-reframe", "transcription", "captions"}
	if len(m.Chain) != len(want) {
		t.Fatalf("chain length = %d", len(m.Chain))
	}
	for i, slug := range want {
		if m.Chain[i].Pipeline != slug {
			t.Fatalf("chain[%d] = %s, want %s", i, m.Chain[i].Pipeline, slug)
		}
	}
}
