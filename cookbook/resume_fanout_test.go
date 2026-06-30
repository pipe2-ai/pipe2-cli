package cookbook

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fanoutMock hands back a unique run id per dispatch so the test can tell each
// fan-out branch's run apart. dispatches counts live dispatches (0 after a full
// resume); it's atomic so the concurrent test can read it race-free.
type fanoutMock struct{ dispatches atomic.Int64 }

func (m *fanoutMock) RunPipeline(_ context.Context, slug string, _ json.RawMessage) (string, error) {
	n := m.dispatches.Add(1)
	return fmt.Sprintf("run-%s-%d", slug, n), nil
}
func (m *fanoutMock) WaitRun(_ context.Context, runID string, _ time.Duration) (*RunRow, error) {
	return &RunRow{ID: runID, Status: "completed", Output: map[string]any{"url": "https://cdn.test/" + runID + ".mp4"}}, nil
}
func (m *fanoutMock) UploadAsset(_ context.Context, p string) (string, error) {
	return "https://up.test/" + p, nil
}
func (m *fanoutMock) EstimatePipelineCost(_ context.Context, _ string, _ json.RawMessage) (*PipelineCostEstimate, error) {
	return nil, nil
}

// runBranches simulates a fan-out: two Substep branches each running the SAME
// pipeline at the same step index (both start from the parent's stepCounter).
// Returns each branch's run id keyed by its substep prefix.
func runBranches(t *testing.T, c *Context) map[string]string {
	t.Helper()
	got := map[string]string{}
	for i := 1; i <= 2; i++ {
		sub := c.Substep(i)
		res, err := sub.RunPipeline("video-reframe", Inputs{"clip": i})
		if err != nil {
			t.Fatalf("substep %d: %v", i, err)
		}
		got[sub.substepPrefix] = res.RunID
	}
	return got
}

// TestResumeFanoutKeying guards the clip-keying bug: fan-out branches share a
// step Idx, so keying resume/state on Idx alone made every branch reuse the
// first branch's run (and clobber each other's state.json entry).
func TestResumeFanoutKeying(t *testing.T) {
	dir := t.TempDir()
	mc := &fanoutMock{}
	c := NewContext(context.Background(), mc, map[string]any{},
		WithCaptureDir(dir), WithRecipeSlug("clip-factory"))

	first := runBranches(t, c)
	if first["substep-1"] == "" || first["substep-1"] == first["substep-2"] {
		t.Fatalf("branches should dispatch distinct runs, got %v", first)
	}

	// state.json holds ONE entry per branch — same Idx, distinct Substep.
	state := readState(t, filepath.Join(dir, "state.json"))
	if len(state.Steps) != 2 {
		t.Fatalf("want 2 recorded steps (one per branch), got %d: %+v", len(state.Steps), state.Steps)
	}
	for _, s := range state.Steps {
		if s.Idx != 1 {
			t.Errorf("fan-out step should be idx 1, got %d", s.Idx)
		}
		if first[s.Substep] != s.RunID {
			t.Errorf("state %q recorded run %q, dispatched %q", s.Substep, s.RunID, first[s.Substep])
		}
	}

	// Resume: each branch reuses ITS OWN run; nothing new is dispatched.
	mc2 := &fanoutMock{}
	rc := NewContext(context.Background(), mc2, map[string]any{},
		WithCaptureDir(dir), WithRecipeSlug("clip-factory"), WithResume(state))
	second := runBranches(t, rc)
	if n := mc2.dispatches.Load(); n != 0 {
		t.Errorf("resume should dispatch no new runs, dispatched %d", n)
	}
	for k := range first {
		if second[k] != first[k] {
			t.Errorf("branch %q resumed run %q, want its own %q (clip-keying bug)", k, second[k], first[k])
		}
	}
}

// TestRecordStepConcurrentFanout exercises the shared stateMu: real recipes
// fan out via errgroup, so branches recordStep concurrently into one
// state.json. Without the lock the read-modify-write loses updates and fewer
// than N entries land — a file-level race the -race detector can't see, so the
// count assertion is the real guard.
func TestRecordStepConcurrentFanout(t *testing.T) {
	const branches = 12
	dir := t.TempDir()
	mc := &fanoutMock{}
	c := NewContext(context.Background(), mc, map[string]any{},
		WithCaptureDir(dir), WithRecipeSlug("clip-factory"))

	var wg sync.WaitGroup
	for i := 1; i <= branches; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sub := c.Substep(i)
			if _, err := sub.RunPipeline("video-reframe", Inputs{"clip": i}); err != nil {
				t.Errorf("substep %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	state := readState(t, filepath.Join(dir, "state.json"))
	if len(state.Steps) != branches {
		t.Fatalf("want %d entries (no lost updates), got %d", branches, len(state.Steps))
	}
	seen := map[string]bool{}
	for _, s := range state.Steps {
		if seen[s.Substep] {
			t.Errorf("duplicate substep entry %q", s.Substep)
		}
		seen[s.Substep] = true
	}
}

func readState(t *testing.T, path string) *ResumeState {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var s ResumeState
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	return &s
}
