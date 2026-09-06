package cookbook

import (
	"context"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestCaptureDryRunDoesNotFetchOrWrite(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("must not fetch"))
	}))
	defer server.Close()
	dir := t.TempDir()
	ctx := NewContext(context.Background(), nil, nil, WithDryRun(true), WithCaptureDir(dir))
	for _, capture := range []func() error{
		func() error { return ctx.Capture(1, "dry://step-1") },
		func() error { return ctx.Capture(2, server.URL+"/image.png") },
		func() error { return ctx.CaptureFinal(server.URL + "/video.mp4") },
	} {
		if err := capture(); err != nil {
			t.Fatal(err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("dry-run fetched %d assets", requests.Load())
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("dry-run created files: %v, %v", entries, err)
	}
}

func TestCaptureFinalWritesHero(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("final video"))
	}))
	defer server.Close()

	dir := t.TempDir()
	ctx := NewContext(context.Background(), nil, nil, WithCaptureDir(dir))
	if err := ctx.CaptureFinal(server.URL + "/output.mp4"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "hero.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "final video" {
		t.Fatalf("hero content = %q", got)
	}
}

func TestCaptureFinalIgnoresStructuredOutput(t *testing.T) {
	dir := t.TempDir()
	ctx := NewContext(context.Background(), nil, nil, WithCaptureDir(dir))
	if err := ctx.CaptureFinal(`["https://example.com/a.mp4"]`); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("captured %d files for structured output", len(entries))
	}
}

func TestCaptureUsesImageBytesAndReplacesOnlyPrimaryArtifacts(t *testing.T) {
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail.Load() {
			http.Error(w, "failed", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "image/png") // Both header and URL lie.
		_ = jpeg.Encode(w, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil)
	}))
	defer server.Close()
	dir := t.TempDir()
	for _, name := range []string{"step-1.png", "step-1.poster.jpg", "step-2.png"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("previous"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := NewContext(context.Background(), nil, nil, WithCaptureDir(dir))
	url := server.URL + "/output.png?signature=value"
	for _, capture := range []func() error{
		func() error { return ctx.Capture(1, url) },
		func() error { return ctx.CaptureFinal(url) },
		func() error { return ctx.Substep(2).Capture(1, url) },
	} {
		if err := capture(); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"step-1.jpg", "hero.jpg", "substep-2-step-1.jpg"} {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		_, err = jpeg.Decode(f)
		f.Close()
		if err != nil {
			t.Fatalf("%s is not JPEG: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "step-1.png")); !os.IsNotExist(err) {
		t.Fatalf("stale primary still exists: %v", err)
	}
	fail.Store(true)
	if err := ctx.Capture(1, url); err == nil {
		t.Fatal("failed recapture succeeded")
	}
	for _, name := range []string{"step-1.jpg", "step-1.poster.jpg", "step-2.png"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("unrelated or previous accepted artifact %s removed: %v", name, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 5 {
		t.Fatalf("unexpected leftover capture files: %v, %v", entries, err)
	}
}
