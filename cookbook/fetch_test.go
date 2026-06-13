package cookbook

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifySource(t *testing.T) {
	const storage = "https://assets.pipe2.ai"
	cases := []struct {
		name    string
		raw     string
		noFetch bool
		want    sourceKind
	}{
		{"empty", "", false, sourceEmpty},
		{"s3 path", "/s3/pipe2-ai/x.mp4", false, sourceExistingAsset},
		{"own storage host", "https://assets.pipe2.ai/generated/x.mp4", false, sourceExistingAsset},
		{"remote youtube", "https://www.youtube.com/watch?v=abc", false, sourceRemote},
		{"remote direct mp4", "https://cdn.example.com/a.mp4", false, sourceRemote},
		{"local path", "./clip.mp4", false, sourceLocalPath},
		{"local absolute", "/home/u/clip.mp4", false, sourceLocalPath},
		{"no-fetch remote", "https://www.youtube.com/watch?v=abc", true, sourceExistingAsset},
		{"no-fetch bare id", "8d9f-asset-id", true, sourceExistingAsset},
		{"no-fetch local", "./clip.mp4", true, sourceExistingAsset},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifySource(tc.raw, storage, tc.noFetch); got != tc.want {
				t.Fatalf("classifySource(%q, noFetch=%v) = %d, want %d", tc.raw, tc.noFetch, got, tc.want)
			}
		})
	}
}

func TestClassifySourceEmptyStorageBase(t *testing.T) {
	// With no storage base configured, an http(s) URL can't be matched to
	// our own host, so every remote URL is treated as a fetch target.
	if got := classifySource("https://assets.pipe2.ai/x.mp4", "", false); got != sourceRemote {
		t.Fatalf("got %d, want sourceRemote when storageBase empty", got)
	}
}

func TestIsDirectMediaURL(t *testing.T) {
	yes := []string{
		"https://cdn.example.com/a.mp4",
		"https://cdn.example.com/path/clip.MOV",
		"https://x/y.webm?token=abc",
		"https://x/y.m4a",
	}
	no := []string{
		"https://www.youtube.com/watch?v=abc",
		"https://vimeo.com/12345",
		"https://example.com/video",
		"https://example.com/page.html",
	}
	for _, u := range yes {
		if !isDirectMediaURL(u) {
			t.Errorf("isDirectMediaURL(%q) = false, want true", u)
		}
	}
	for _, u := range no {
		if isDirectMediaURL(u) {
			t.Errorf("isDirectMediaURL(%q) = true, want false", u)
		}
	}
}

func TestHTTPDownloadMediaSuccess(t *testing.T) {
	body := []byte("\x00\x00\x00\x18ftypmp42fake-media-bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ua := r.Header.Get("User-Agent"); !strings.Contains(ua, "Mozilla") {
			t.Errorf("expected browser UA, got %q", ua)
		}
		w.Header().Set("Content-Type", "video/mp4")
		w.Write(body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	path, err := httpDownloadMedia(context.Background(), srv.URL+"/clip.mp4", dir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("downloaded body mismatch")
	}
	if filepath.Base(path) != "clip.mp4" {
		t.Errorf("expected temp file named clip.mp4, got %q", filepath.Base(path))
	}
}

func TestHTTPDownloadMediaNon2xxFailsLoudly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("denied"))
	}))
	defer srv.Close()

	_, err := httpDownloadMedia(context.Background(), srv.URL+"/forbidden.mp4", t.TempDir(), nil)
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Fatalf("error should mention the status code, got: %v", err)
	}
}

func TestHTTPDownloadMediaNonMediaBodyFailsLoudly(t *testing.T) {
	// A 200 that returns an HTML page (consent/error) is the exact trap that
	// produced "ffprobe: Invalid data" — reject it before it can be uploaded.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte("<html><body>are you a robot?</body></html>"))
	}))
	defer srv.Close()

	_, err := httpDownloadMedia(context.Background(), srv.URL+"/clip.mp4", t.TempDir(), nil)
	if err == nil {
		t.Fatal("expected error on non-media body")
	}
	if !strings.Contains(err.Error(), "non-media") {
		t.Fatalf("error should flag non-media content-type, got: %v", err)
	}
}

func TestHTTPDownloadMediaOctetStreamAllowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte("media"))
	}))
	defer srv.Close()

	if _, err := httpDownloadMedia(context.Background(), srv.URL+"/clip.mp4", t.TempDir(), nil); err != nil {
		t.Fatalf("octet-stream media should be allowed, got: %v", err)
	}
}

func TestFetchRemoteSourceWrapsAndCleansUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	_, cleanup, err := fetchRemoteSource(context.Background(), srv.URL+"/clip.mp4", nil)
	if cleanup == nil {
		t.Fatal("cleanup must never be nil")
	}
	cleanup() // safe to call on error
	var sfe *SourceFetchError
	if !errors.As(err, &sfe) {
		t.Fatalf("expected *SourceFetchError, got %T: %v", err, err)
	}
	if !strings.HasPrefix(err.Error(), "source fetch failed") {
		t.Fatalf("error must start with 'source fetch failed', got: %v", err)
	}
}

func TestFetchRemoteSourceCleansUpTempDirOnSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Write([]byte("media"))
	}))
	defer srv.Close()

	path, cleanup, err := fetchRemoteSource(context.Background(), srv.URL+"/clip.mp4", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("downloaded file should exist before cleanup: %v", err)
	}
	dir := filepath.Dir(path)
	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("temp dir should be removed after cleanup, stat err = %v", err)
	}
}

func TestYtdlpMissingGivesActionableError(t *testing.T) {
	// Force yt-dlp to be unfindable so we exercise the missing-dependency
	// branch deterministically regardless of the host.
	t.Setenv("PATH", "")
	_, err := ytdlpDownload(context.Background(), "https://www.youtube.com/watch?v=abc", t.TempDir(), nil)
	if err == nil {
		t.Fatal("expected error when yt-dlp is absent")
	}
	if !strings.Contains(err.Error(), "yt-dlp") {
		t.Fatalf("error should name yt-dlp, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Install") {
		t.Fatalf("error should be actionable (mention install), got: %v", err)
	}
}
