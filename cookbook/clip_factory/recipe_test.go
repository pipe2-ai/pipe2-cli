package clip_factory_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pipe2-ai/pipe2-cli/cookbook"
	"github.com/pipe2-ai/pipe2-cli/cookbook/clip_factory"
)

// mockClient records every RunPipeline call and replies with a canned
// output keyed by pipeline slug. Implements cookbook.Client.
type mockClient struct {
	t       *testing.T
	calls   []call
	outputs map[string]map[string]any
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
	return "run-" + slug, nil
}

func (m *mockClient) WaitRun(_ context.Context, runID string, _ time.Duration) (*cookbook.RunRow, error) {
	slug := runID[len("run-"):]
	out, ok := m.outputs[slug]
	if !ok {
		m.t.Fatalf("WaitRun: no canned output for %q", slug)
	}
	return &cookbook.RunRow{ID: runID, Status: "completed", Output: out}, nil
}

func (m *mockClient) UploadAsset(_ context.Context, localPath string) (string, error) {
	return "https://uploaded.test/" + localPath, nil
}

func (m *mockClient) EstimatePipelineCost(_ context.Context, _ string, _ json.RawMessage) (*cookbook.PipelineCostEstimate, error) {
	return nil, nil
}

// commonOutputs are the canned pipeline-output maps every test in
// this file shares. The highlights entry uses the 2-moment shape so
// auto-path tests get 2 clips. Tests that use --clips use
// writeClipsJSON (1 moment) instead of this highlights output.
func commonOutputs() map[string]map[string]any {
	return map[string]map[string]any{
		"transcription": {"srt_asset_url": "https://cdn.test/transcript.srt", "txt_asset_url": "https://cdn.test/transcript.txt"},
		"highlights": {
			"highlights_url": "https://cdn.test/highlights.json",
			"count":          float64(2),
			"highlights": []any{
				map[string]any{"context": "Auto pick 1", "start_sec": float64(10.5), "end_sec": float64(22.5)},
				map[string]any{"context": "Auto pick 2", "start_sec": float64(40.0), "end_sec": float64(58.0)},
			},
		},
		"video-trim":    {"video_url": "https://cdn.test/trimmed.mp4", "transcript_url": "https://cdn.test/trimmed.srt"},
		"video-reframe": {"video_url": "https://cdn.test/reframed.mp4", "subject_y_pct": 0.5},
		"captions":      {"video_url": "https://cdn.test/captioned.mp4"},
		"watermark":     {"video_url": "https://cdn.test/watermarked.mp4"},
	}
}

// writeClipsJSON dumps a one-moment clips file into tmp so each test
// gets its own fresh path without sharing fixtures.
func writeClipsJSON(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clips.json")
	body := `[{"context": "test moment", "start_sec": 5.0, "end_sec": 17.0}]`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write clips: %v", err)
	}
	return path
}

// dispatchInputs builds the input map for cookbook.ResolveInputs in
// one place so each test reads as a focused override of the relevant
// flag(s). No --clips by default — tests that want manual override
// pass it explicitly.
func dispatchInputs(t *testing.T, overrides map[string]string) cookbook.Inputs {
	t.Helper()
	flags := map[string]string{
		// An existing-asset reference ("/s3/...") so ResolveSourceURL passes
		// it straight through — these tests exercise orchestration, not the
		// remote-fetch path (that's covered by ResolveSourceURL tests and
		// cookbook/fetch_test.go). A remote URL here would trigger a real
		// client-side download.
		"--input":    "/s3/test-bucket/source.mp4",
		"--preset":   "karaoke-gradient",
		"--lang":     "en",
		"--parallel": "1",
	}
	for k, v := range overrides {
		flags[k] = v
	}
	inputs, err := cookbook.ResolveInputs((&clip_factory.Recipe{}).Manifest(), flags)
	if err != nil {
		t.Fatalf("resolve inputs: %v", err)
	}
	return inputs
}

// callBySlug returns the first dispatch call matching slug, or fails
// the test.
func callBySlug(t *testing.T, calls []call, slug string) call {
	t.Helper()
	for _, c := range calls {
		if c.slug == slug {
			return c
		}
	}
	t.Fatalf("no %q call in %v", slug, callSlugs(calls))
	return call{}
}

// callSlugs returns the ordered list of pipeline slugs that fired.
func callSlugs(calls []call) []string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.slug
	}
	return out
}

// countBySlug returns how many times slug appears in calls.
func countBySlug(calls []call, slug string) int {
	n := 0
	for _, c := range calls {
		if c.slug == slug {
			n++
		}
	}
	return n
}

// TestRun_NoClips_RunsHighlightsAuto verifies the default auto-pick
// path: no --clips → highlights fires and 2 moments → 2 trim+captions+watermark sets.
func TestRun_NoClips_RunsHighlightsAuto(t *testing.T) {
	mc := &mockClient{t: t, outputs: commonOutputs()}
	r := &clip_factory.Recipe{}

	inputs := dispatchInputs(t, nil)
	ctx := cookbook.NewContext(context.Background(), mc, inputs)
	if err := r.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	slugs := callSlugs(mc.calls)
	// Must have highlights.
	if countBySlug(mc.calls, "highlights") != 1 {
		t.Errorf("expected 1 highlights call, got %d (chain: %v)", countBySlug(mc.calls, "highlights"), slugs)
	}
	// Must NOT have video-reframe (no --reformat).
	if countBySlug(mc.calls, "video-reframe") != 0 {
		t.Errorf("unexpected video-reframe calls (chain: %v)", slugs)
	}
	// 2 moments → 2 video-trim, 2 captions, 2 watermark.
	if countBySlug(mc.calls, "video-trim") != 2 {
		t.Errorf("expected 2 video-trim calls, got %d (chain: %v)", countBySlug(mc.calls, "video-trim"), slugs)
	}
	if countBySlug(mc.calls, "captions") != 2 {
		t.Errorf("expected 2 captions calls, got %d (chain: %v)", countBySlug(mc.calls, "captions"), slugs)
	}
	if countBySlug(mc.calls, "watermark") != 2 {
		t.Errorf("expected 2 watermark calls, got %d (chain: %v)", countBySlug(mc.calls, "watermark"), slugs)
	}
}

// TestRun_WithClips_SkipsHighlights verifies the manual override path:
// --clips set → highlights is skipped entirely.
func TestRun_WithClips_SkipsHighlights(t *testing.T) {
	mc := &mockClient{t: t, outputs: commonOutputs()}
	r := &clip_factory.Recipe{}

	inputs := dispatchInputs(t, map[string]string{"--clips": writeClipsJSON(t)})
	ctx := cookbook.NewContext(context.Background(), mc, inputs)
	if err := r.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	slugs := callSlugs(mc.calls)
	if countBySlug(mc.calls, "highlights") != 0 {
		t.Errorf("highlights must be skipped when --clips is set (chain: %v)", slugs)
	}
	// 1 moment from clips file → exactly 1 trim.
	if countBySlug(mc.calls, "video-trim") != 1 {
		t.Errorf("expected 1 video-trim call, got %d (chain: %v)", countBySlug(mc.calls, "video-trim"), slugs)
	}
	// Chain: transcription, video-trim, captions, watermark — no highlights.
	want := []string{"transcription", "video-trim", "captions", "watermark"}
	got := slugs
	if len(got) != len(want) {
		t.Fatalf("chain = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("chain[%d] = %q, want %q", i, got[i], w)
		}
	}
}

// TestRun_Reformat9x16_DispatchesReframe verifies the optional reframe
// path with auto highlights (2 moments → 2 reframe calls).
func TestRun_Reformat9x16_DispatchesReframe(t *testing.T) {
	mc := &mockClient{t: t, outputs: commonOutputs()}
	r := &clip_factory.Recipe{}

	inputs := dispatchInputs(t, map[string]string{"--reformat": "9:16"})
	ctx := cookbook.NewContext(context.Background(), mc, inputs)
	if err := r.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// highlights + 2×(trim+reframe+captions+watermark)
	if countBySlug(mc.calls, "highlights") != 1 {
		t.Errorf("expected 1 highlights, got %d", countBySlug(mc.calls, "highlights"))
	}
	if countBySlug(mc.calls, "video-reframe") != 2 {
		t.Errorf("expected 2 video-reframe calls, got %d", countBySlug(mc.calls, "video-reframe"))
	}

	// Verify the reframe got the right input shape.
	reframeIn := callBySlug(t, mc.calls, "video-reframe").inputs
	if got := reframeIn["target_aspect_ratio"]; got != "9:16" {
		t.Errorf("reframe target_aspect_ratio = %q, want 9:16", got)
	}
	// The windowed transcript from video-trim flows into reframe so the
	// V4 camera director's transcript-driven speaker focal fires per clip.
	if got := reframeIn["transcript_url"]; got != "https://cdn.test/trimmed.srt" {
		t.Errorf("reframe transcript_url = %q, want the trim's windowed SRT", got)
	}
}

// TestRun_PositionAuto_ForwardsSubjectHint pins the recipe's contract
// with the captions pipeline: when --reformat fires, the recipe must
// forward the reframe step's subject_y_pct.
func TestRun_PositionAuto_ForwardsSubjectHint(t *testing.T) {
	outputs := commonOutputs()
	outputs["video-reframe"] = map[string]any{
		"video_url":     "https://cdn.test/reframed.mp4",
		"subject_y_pct": 0.30,
	}
	mc := &mockClient{t: t, outputs: outputs}
	inputs := dispatchInputs(t, map[string]string{"--reformat": "9:16"})
	ctx := cookbook.NewContext(context.Background(), mc, inputs)
	if err := (&clip_factory.Recipe{}).Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	captionsIn := callBySlug(t, mc.calls, "captions").inputs
	if got := captionsIn["position"]; got != "auto" {
		t.Errorf("captions position = %v, want %q (recipe forwards verbatim)", got, "auto")
	}
	if got := captionsIn["subject_y_pct"]; got != 0.30 {
		t.Errorf("captions subject_y_pct = %v, want 0.30 (forwarded from reframe)", got)
	}
}

// TestRun_PositionAuto_NoReformatOmitsHint covers the native-aspect
// path: reframe never runs, subject_y_pct=0 sent to captions.
func TestRun_PositionAuto_NoReformatOmitsHint(t *testing.T) {
	mc := &mockClient{t: t, outputs: commonOutputs()}
	inputs := dispatchInputs(t, nil)
	ctx := cookbook.NewContext(context.Background(), mc, inputs)
	if err := (&clip_factory.Recipe{}).Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	captionsIn := callBySlug(t, mc.calls, "captions").inputs
	if got := captionsIn["position"]; got != "auto" {
		t.Errorf("captions position = %v, want %q", got, "auto")
	}
	if got := captionsIn["subject_y_pct"]; got != 0.0 {
		t.Errorf("captions subject_y_pct = %v, want 0 (no reframe → no hint)", got)
	}
}

// TestRun_PositionExplicit_PassesThrough verifies explicit user
// positions reach the captions pipeline untouched.
func TestRun_PositionExplicit_PassesThrough(t *testing.T) {
	mc := &mockClient{t: t, outputs: commonOutputs()}
	inputs := dispatchInputs(t, map[string]string{
		"--reformat": "9:16",
		"--position": "bottom",
	})
	ctx := cookbook.NewContext(context.Background(), mc, inputs)
	if err := (&clip_factory.Recipe{}).Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	captionsIn := callBySlug(t, mc.calls, "captions").inputs
	if got := captionsIn["position"]; got != "bottom" {
		t.Errorf("captions position = %v, want %q", got, "bottom")
	}
}

// TestRun_CorrectionsRouteToTranscription pins the architectural
// choice that corrections land at the transcription step only.
func TestRun_CorrectionsRouteToTranscription(t *testing.T) {
	mc := &mockClient{t: t, outputs: commonOutputs()}
	inputs := dispatchInputs(t, map[string]string{
		"--corrections": "Qube=Cube,Kyub=Cube",
	})
	ctx := cookbook.NewContext(context.Background(), mc, inputs)
	if err := (&clip_factory.Recipe{}).Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	transcribeIn := mc.calls[0].inputs
	corrections, _ := transcribeIn["corrections"].(map[string]any)
	if got, want := corrections["Qube"], "Cube"; got != want {
		t.Errorf("transcription corrections[Qube] = %v, want %q", got, want)
	}
	captionsIn := callBySlug(t, mc.calls, "captions").inputs
	if _, exists := captionsIn["corrections"]; exists {
		t.Errorf("captions inputs unexpectedly carry corrections: %v", captionsIn)
	}
}

// TestRun_HighlightsProvidesExplicitTimestamps verifies the auto path
// forwards start_sec + end_sec from highlights into video-trim and does
// not include LLM-path fields (context, desired_seconds).
func TestRun_HighlightsProvidesExplicitTimestamps(t *testing.T) {
	mc := &mockClient{t: t, outputs: commonOutputs()}
	r := &clip_factory.Recipe{}

	inputs := dispatchInputs(t, nil)
	ctx := cookbook.NewContext(context.Background(), mc, inputs)
	if err := r.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	trimCall := callBySlug(t, mc.calls, "video-trim")

	if got := trimCall.inputs["start_sec"]; got != float64(10.5) {
		t.Errorf("video-trim start_sec = %v, want 10.5", got)
	}
	if got := trimCall.inputs["end_sec"]; got != float64(22.5) {
		t.Errorf("video-trim end_sec = %v, want 22.5", got)
	}
	for _, dead := range []string{"context", "desired_seconds"} {
		if v, ok := trimCall.inputs[dead]; ok {
			t.Errorf("video-trim inputs carry %s=%v — LLM-path field leaked into explicit-window dispatch", dead, v)
		}
	}
}

// TestRun_ManualClips_ForwardsExplicitTimestamps verifies a manual
// --clips entry with start_sec/end_sec reaches video-trim verbatim.
// The recipe no longer supports an LLM fallback — entries without
// timestamps are rejected (covered by TestRun_ManualClipsWithoutTimestamps_Fails).
func TestRun_ManualClips_ForwardsExplicitTimestamps(t *testing.T) {
	mc := &mockClient{t: t, outputs: commonOutputs()}
	r := &clip_factory.Recipe{}

	inputs := dispatchInputs(t, map[string]string{"--clips": writeClipsJSON(t)})
	ctx := cookbook.NewContext(context.Background(), mc, inputs)
	if err := r.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	trimCall := callBySlug(t, mc.calls, "video-trim")
	if got := trimCall.inputs["start_sec"]; got != float64(5.0) {
		t.Errorf("video-trim start_sec = %v, want 5.0", got)
	}
	if got := trimCall.inputs["end_sec"]; got != float64(17.0) {
		t.Errorf("video-trim end_sec = %v, want 17.0", got)
	}
	for _, dead := range []string{"context", "desired_seconds"} {
		if v, ok := trimCall.inputs[dead]; ok {
			t.Errorf("video-trim inputs carry %s=%v — LLM-path field leaked into manual-clip dispatch", dead, v)
		}
	}
}

// TestRun_ManualClipsWithoutTimestamps_Fails pins the new contract: a
// --clips entry that omits start_sec/end_sec must error before any trim
// call — video-trim has no LLM fallback to recover with.
func TestRun_ManualClipsWithoutTimestamps_Fails(t *testing.T) {
	mc := &mockClient{t: t, outputs: commonOutputs()}
	r := &clip_factory.Recipe{}

	path := filepath.Join(t.TempDir(), "clips-no-times.json")
	if err := os.WriteFile(path, []byte(`[{"context": "no times"}]`), 0o644); err != nil {
		t.Fatalf("write clips: %v", err)
	}

	inputs := dispatchInputs(t, map[string]string{"--clips": path})
	ctx := cookbook.NewContext(context.Background(), mc, inputs)
	if err := r.Run(ctx); err == nil {
		t.Fatalf("Run: expected error for clip without timestamps, got nil")
	}
	if countBySlug(mc.calls, "video-trim") != 0 {
		t.Errorf("video-trim must not be dispatched when timestamps are missing (chain: %v)", callSlugs(mc.calls))
	}
}

// TestManifest_Shape is the public-API contract test for /recipes/
// page rendering and the LLM-friendly cookbook export.
func TestManifest_Shape(t *testing.T) {
	m := (&clip_factory.Recipe{}).Manifest()
	if m.Slug != "clip-factory" {
		t.Errorf("Slug = %q, want clip-factory", m.Slug)
	}
	// Chain is now 6 steps: transcription, highlights, video-trim, video-reframe, captions, watermark.
	if len(m.Chain) != 6 {
		t.Fatalf("Chain length = %d, want 6", len(m.Chain))
	}
	wantSlugs := []string{"transcription", "highlights", "video-trim", "video-reframe", "captions", "watermark"}
	for i, want := range wantSlugs {
		if m.Chain[i].Pipeline != want {
			t.Errorf("Chain[%d].Pipeline = %q, want %q", i, m.Chain[i].Pipeline, want)
		}
	}
	// highlights step must declare OptionalWhenEmpty: "clips"
	if m.Chain[1].OptionalWhenEmpty != "clips" {
		t.Errorf("Chain[1] (highlights) OptionalWhenEmpty = %q, want %q", m.Chain[1].OptionalWhenEmpty, "clips")
	}
	// reframe step must declare OptionalWhenEmpty: "reformat"
	if m.Chain[3].OptionalWhenEmpty != "reformat" {
		t.Errorf("Chain[3] (reframe) OptionalWhenEmpty = %q, want %q", m.Chain[3].OptionalWhenEmpty, "reformat")
	}
	// Only "source" must be Required; "clips" is now optional.
	requiredFound := map[string]bool{}
	for _, in := range m.Inputs {
		if in.Required {
			requiredFound[in.Name] = true
		}
	}
	if !requiredFound["source"] {
		t.Errorf("input %q must be Required", "source")
	}
	if requiredFound["clips"] {
		t.Errorf("input %q must NOT be Required (it is the manual override, default empty)", "clips")
	}
	// New inputs must be present.
	wantKnobs := map[string]bool{
		"reformat":         false,
		"corrections":      false,
		"position":         false,
		"highlights_count": false,
		"highlights_style": false,
	}
	for _, in := range m.Inputs {
		if _, ok := wantKnobs[in.Name]; ok {
			wantKnobs[in.Name] = true
		}
	}
	for name, found := range wantKnobs {
		if !found {
			t.Errorf("Manifest missing input %q", name)
		}
	}
}
