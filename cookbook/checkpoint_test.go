package cookbook

import (
	"context"
	"errors"
	"testing"
)

func TestCheckpointStopsBeforeVideoAndResumes(t *testing.T) {
	dir := t.TempDir()
	client := &fanoutMock{}
	c := NewContext(context.Background(), client, nil, WithCaptureDir(dir), WithStopBeforeStep(2))
	if _, err := c.RunPipeline("image-generator", Inputs{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RunPipeline("video-generator", Inputs{}); !errors.Is(err, ErrCheckpoint) {
		t.Fatalf("video was not stopped: %v", err)
	}
	if client.dispatches.Load() != 1 {
		t.Fatal("checkpoint spent on video")
	}
	state, err := LoadResumeState(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := NewContext(context.Background(), client, nil, WithResume(state))
	if _, err := r.RunPipeline("image-generator", Inputs{}); err != nil {
		t.Fatal(err)
	}
	if client.dispatches.Load() != 1 {
		t.Fatal("resume regenerated image")
	}
	if _, err := r.RunPipeline("video-generator", Inputs{}); err != nil {
		t.Fatal(err)
	}
	if client.dispatches.Load() != 2 {
		t.Fatal("resume did not dispatch video")
	}
	changed := NewContext(context.Background(), client, nil, WithResume(state))
	if _, err := changed.RunPipeline("image-generator", Inputs{"prompt": "different"}); err == nil {
		t.Fatal("changed inputs reused approved state")
	}
	if client.dispatches.Load() != 2 {
		t.Fatal("changed inputs spent credits")
	}
	dry := NewContext(context.Background(), nil, nil, WithDryRun(true), WithStopBeforeStep(1))
	if _, err := dry.RunPipeline("video-generator", Inputs{}); !errors.Is(err, ErrCheckpoint) {
		t.Fatal("dry-run crossed estimate boundary")
	}
}
