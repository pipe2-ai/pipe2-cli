package cookbook

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// resolveMockClient is a minimal cookbook.Client for exercising
// ResolveSourceURL. Only UploadAsset is used here.
type resolveMockClient struct {
	uploaded []string
}

func (m *resolveMockClient) RunPipeline(context.Context, string, json.RawMessage) (string, error) {
	return "", nil
}
func (m *resolveMockClient) WaitRun(context.Context, string, time.Duration) (*RunRow, error) {
	return nil, nil
}
func (m *resolveMockClient) UploadAsset(_ context.Context, localPath string) (string, error) {
	m.uploaded = append(m.uploaded, localPath)
	return "https://assets.pipe2.ai/uploaded/" + localPath, nil
}
func (m *resolveMockClient) EstimatePipelineCost(context.Context, string, json.RawMessage) (*PipelineCostEstimate, error) {
	return nil, nil
}

func newResolveContext(t *testing.T, mc Client, raw string, opts ...ContextOption) *Context {
	t.Helper()
	base := []ContextOption{WithStorageURL("https://assets.pipe2.ai")}
	return NewContext(context.Background(), mc, map[string]any{"source": raw}, append(base, opts...)...)
}

func mustResolve(t *testing.T, ctx *Context) string {
	t.Helper()
	got, err := ctx.ResolveSourceURL("source")
	if err != nil {
		t.Fatalf("ResolveSourceURL: unexpected error: %v", err)
	}
	return got
}

func TestResolveSourceURL_PassThroughExistingAssets(t *testing.T) {
	for _, raw := range []string{
		"/s3/bucket/clip.mp4",
		"https://assets.pipe2.ai/generated/clip.mp4", // our own storage host
	} {
		mc := &resolveMockClient{}
		ctx := newResolveContext(t, mc, raw)
		if got := mustResolve(t, ctx); got != raw {
			t.Errorf("ResolveSourceURL(%q) = %q, want pass-through", raw, got)
		}
		if len(mc.uploaded) != 0 {
			t.Errorf("existing asset %q should not upload, uploaded=%v", raw, mc.uploaded)
		}
	}
}

func TestResolveSourceURL_Empty(t *testing.T) {
	ctx := newResolveContext(t, &resolveMockClient{}, "")
	if got := mustResolve(t, ctx); got != "" {
		t.Errorf("empty source = %q, want \"\"", got)
	}
}

func TestResolveSourceURL_RemoteFetchesThenUploads(t *testing.T) {
	mc := &resolveMockClient{}
	var fetched string
	stub := func(_ context.Context, rawURL string, _ progressFunc) (string, func(), error) {
		fetched = rawURL
		return "/tmp/fetched-source.mp4", func() {}, nil
	}
	ctx := newResolveContext(t, mc, "https://www.youtube.com/watch?v=abc", WithFetcher(stub))

	got := mustResolve(t, ctx)
	if fetched != "https://www.youtube.com/watch?v=abc" {
		t.Errorf("fetcher saw %q, want the youtube URL", fetched)
	}
	if len(mc.uploaded) != 1 || mc.uploaded[0] != "/tmp/fetched-source.mp4" {
		t.Errorf("expected the fetched temp file to be uploaded, uploaded=%v", mc.uploaded)
	}
	if !strings.HasPrefix(got, "https://assets.pipe2.ai/uploaded/") {
		t.Errorf("source should resolve to the uploaded asset URL, got %q", got)
	}
}

func TestResolveSourceURL_RemoteFetchFailureReturnsCleanError(t *testing.T) {
	cleanedUp := false
	stub := func(_ context.Context, rawURL string, _ progressFunc) (string, func(), error) {
		return "", func() { cleanedUp = true }, &SourceFetchError{URL: rawURL, Err: errBoom}
	}
	ctx := newResolveContext(t, &resolveMockClient{}, "https://www.youtube.com/watch?v=abc", WithFetcher(stub))

	got, err := ctx.ResolveSourceURL("source")
	if err == nil {
		t.Fatal("expected an error on fetch failure, not a panic")
	}
	if got != "" {
		t.Errorf("expected empty url on error, got %q", got)
	}
	if !strings.Contains(err.Error(), "source fetch failed") {
		t.Fatalf("error should carry the 'source fetch failed' message, got: %v", err)
	}
	if !cleanedUp {
		t.Error("temp download must be cleaned up even on failure")
	}
}

func TestResolveSourceURL_NoFetchPassesThrough(t *testing.T) {
	mc := &resolveMockClient{}
	calledFetcher := false
	stub := func(_ context.Context, rawURL string, _ progressFunc) (string, func(), error) {
		calledFetcher = true
		return "", func() {}, nil
	}
	// --no-fetch: a remote URL (or bare asset id) is treated as already an
	// asset and passed through verbatim.
	raw := "https://www.youtube.com/watch?v=abc"
	ctx := newResolveContext(t, mc, raw, WithNoFetch(true), WithFetcher(stub))
	if got := mustResolve(t, ctx); got != raw {
		t.Errorf("--no-fetch source = %q, want pass-through %q", got, raw)
	}
	if calledFetcher {
		t.Error("--no-fetch must not invoke the fetcher")
	}
	if len(mc.uploaded) != 0 {
		t.Errorf("--no-fetch must not upload, uploaded=%v", mc.uploaded)
	}
}

func TestResolveSourceURL_LocalPathUploads(t *testing.T) {
	mc := &resolveMockClient{}
	ctx := newResolveContext(t, mc, "./local-clip.mp4")
	got := mustResolve(t, ctx)
	if len(mc.uploaded) != 1 || mc.uploaded[0] != "./local-clip.mp4" {
		t.Errorf("local path should upload, uploaded=%v", mc.uploaded)
	}
	if got != "https://assets.pipe2.ai/uploaded/./local-clip.mp4" {
		t.Errorf("unexpected resolved URL %q", got)
	}
}

func TestResolveSourceURL_DryRunNoNetwork(t *testing.T) {
	mc := &resolveMockClient{}
	calledFetcher := false
	stub := func(_ context.Context, _ string, _ progressFunc) (string, func(), error) {
		calledFetcher = true
		return "", func() {}, nil
	}
	ctx := newResolveContext(t, mc, "https://www.youtube.com/watch?v=abc", WithDryRun(true), WithFetcher(stub))
	got := mustResolve(t, ctx)
	if calledFetcher {
		t.Error("dry-run must not fetch")
	}
	if len(mc.uploaded) != 0 {
		t.Error("dry-run must not upload")
	}
	if !strings.HasPrefix(got, "dry://") {
		t.Errorf("dry-run should return a dry:// placeholder, got %q", got)
	}
}

var errBoom = boomErr{}

type boomErr struct{}

func (boomErr) Error() string { return "boom" }
