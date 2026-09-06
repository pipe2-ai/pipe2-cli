package question_to_reel_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/pipe2-ai/pipe2-cli/cookbook"
	"github.com/pipe2-ai/pipe2-cli/cookbook/question_to_reel"
	"testing"
	"time"
)

type client struct {
	calls     []string
	inputs    []map[string]any
	short     bool
	landscape bool
}

func (c *client) RunPipeline(_ context.Context, slug string, in json.RawMessage) (string, error) {
	var v map[string]any
	if err := json.Unmarshal(in, &v); err != nil {
		return "", err
	}
	c.calls = append(c.calls, slug)
	c.inputs = append(c.inputs, v)
	return slug, nil
}
func (c *client) WaitRun(_ context.Context, id string, _ time.Duration) (*cookbook.RunRow, error) {
	out := map[string]any{}
	switch id {
	case "audio-generator":
		out["audio_url"] = "https://test/audio.m4a"
	case "transcription":
		out = map[string]any{"duration_sec": 18.5, "txt_asset_url": "https://test/transcript.txt", "srt_asset_url": "https://test/transcript.srt"}
	case "footage-search":
		duration := 15.
		if c.short {
			duration = 2
		}
		clips := []map[string]any{}
		width, height := 1080, 1920
		if c.landscape {
			width, height = 1920, 1080
		}
		for i := 0; i < 2; i++ {
			clips = append(clips, map[string]any{"url": fmt.Sprintf("https://test/clip%d.mp4", i), "duration": duration, "width": width, "height": height, "license": "Test"})
		}
		out["results"] = clips
	default:
		out["video_url"] = "https://test/" + id + ".mp4"
	}
	return &cookbook.RunRow{ID: id, Status: "completed", Output: out}, nil
}
func (*client) UploadAsset(context.Context, string) (string, error) {
	return "", fmt.Errorf("unexpected upload")
}
func (*client) EstimatePipelineCost(context.Context, string, json.RawMessage) (*cookbook.PipelineCostEstimate, error) {
	return nil, nil
}
func TestNarrationControlsAssemblyAndCaptions(t *testing.T) {
	r := &question_to_reel.Recipe{}
	if r.Manifest().Category != "tutorial" {
		t.Fatal("recipe must use the website's tutorial category")
	}
	inputs, err := cookbook.ResolveInputs(r.Manifest(), nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &client{}
	ctx := cookbook.NewContext(context.Background(), c, inputs)
	if err = r.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) != 6 || c.inputs[3]["target_duration_sec"] != 18.5 || c.inputs[3]["narration_url"] != "https://test/audio.m4a" || c.inputs[4]["background_url"] != "https://test/video-reel.mp4" || c.inputs[5]["source_asset_id"] != "https://test/text-card.mp4" || c.inputs[5]["transcript_asset_id"] != "https://test/transcript.srt" || c.inputs[5]["preset_name"] != "tiktok-bold-yellow" || c.inputs[5]["position"] != "bottom" || ctx.FinalOutput() != "https://test/captions.mp4" {
		t.Fatalf("incorrect handoffs: %#v", c.inputs)
	}
	for i, want := range []string{"audio-generator", "transcription", "footage-search", "video-reel", "text-card", "captions"} {
		if c.calls[i] != want {
			t.Fatalf("stage %d: %s", i, c.calls[i])
		}
	}
	c = &client{short: true}
	if err = r.Run(cookbook.NewContext(context.Background(), c, inputs)); err == nil || len(c.calls) != 3 {
		t.Fatalf("short footage should stop before assembly: %v", err)
	}
	c = &client{landscape: true}
	if err = r.Run(cookbook.NewContext(context.Background(), c, inputs)); err == nil || len(c.calls) != 3 {
		t.Fatalf("landscape footage should stop before assembly: %v", err)
	}
	inputs["question"] = "Why is the ocean salty?"
	c = &client{}
	if err = r.Run(cookbook.NewContext(context.Background(), c, inputs)); err == nil || len(c.calls) != 0 {
		t.Fatal("a changed topic must require matching visual beats before spending credits")
	}
}
