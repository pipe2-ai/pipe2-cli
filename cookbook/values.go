package cookbook

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Values is the typed view of resolved recipe inputs the runtime hands
// to a recipe via Context.Inputs. Backed by a map[string]any populated
// by the runner from validated user flags + applied defaults. Each
// accessor coerces to the requested type; mismatches panic since they
// always indicate a recipe bug (the manifest's Input.Type lied about
// what users would supply).
type Values map[string]any

// String returns the value for name as a string. Empty string if absent.
func (v Values) String(name string) string {
	x, ok := v[name]
	if !ok {
		return ""
	}
	s, ok := x.(string)
	if !ok {
		panic(fmt.Sprintf("Values.String(%q): not a string (got %T = %v)", name, x, x))
	}
	return s
}

// Int returns the value for name as int64. Defaults to 0 if absent.
// Coerces float64 (JSON's number type) and the various Go integer
// types because the resolver may hand back any of them depending on
// where the value came from.
func (v Values) Int(name string) int64 {
	x, ok := v[name]
	if !ok {
		return 0
	}
	switch n := x.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	case string:
		// CLI flags come in as strings; the resolver should have
		// coerced already, but accept here for robustness.
		var i int64
		if _, err := fmt.Sscanf(n, "%d", &i); err == nil {
			return i
		}
	}
	panic(fmt.Sprintf("Values.Int(%q): not coercible to int (got %T = %v)", name, x, x))
}

// Bool returns the value for name. False if absent.
func (v Values) Bool(name string) bool {
	x, ok := v[name]
	if !ok {
		return false
	}
	b, ok := x.(bool)
	if !ok {
		panic(fmt.Sprintf("Values.Bool(%q): not a bool (got %T = %v)", name, x, x))
	}
	return b
}

// ResolveAssetURL returns the value for name as a URL ready to pass to a
// pipeline. If the user supplied a local path (anything that doesn't
// start with http(s)://), the runtime uploads it via the CLI's
// existing asset-upload machinery and returns the resulting URL.
//
// Re-used uploads are cached for the lifetime of the Context so two
// inputs pointing at the same local file only upload once per run.
//
// Requires a Client wired into the Context — TestContexts that
// don't set one will panic on a local path. URLs always pass through.
//
// NOTE: a remote http(s) URL passes straight through to the pipeline.
// That is correct for an input that is already an asset URL (e.g. a
// watermark or conditioning asset the user uploaded earlier). For the
// recipe's primary SOURCE media — which the platform must never fetch
// server-side — use ResolveSourceURL instead, which resolves a remote
// URL client-side.
func (c *Context) ResolveAssetURL(name string) string {
	raw := c.Inputs.String(name)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	// Storage-relative URL written by `pipe2 assets upload` and every
	// pipeline activity ("/s3/<bucket>/<key>"). Pipelines accept this
	// form directly (storage.S3Client.ResolveStorageURL expands it);
	// re-uploading it would fail with a stat() error because it's not
	// a local path. Pass through unchanged so a recipe can re-use an
	// already-uploaded source asset without a wasteful re-upload.
	if strings.HasPrefix(raw, "/s3/") {
		return raw
	}
	// Local path → upload. ResolveAssetURL's contract is to panic on
	// failure (a watermark/conditioning input that won't upload is a hard
	// stop with nowhere sensible to return an error to). The recipe's
	// SOURCE media uses ResolveSourceURL, which returns the error instead.
	url, err := c.uploadCached(raw)
	if err != nil {
		panic(fmt.Sprintf("Context.ResolveAssetURL(%q): %v", name, err))
	}
	return url
}

// ResolveSourceURL resolves the recipe's primary source media input. The
// platform never fetches external URLs server-side (the worker's
// datacenter IP is 403'd by YouTube, and a naive GET of a watch page
// hands ffprobe a non-media body), so a remote source is resolved here on
// the user's machine:
//
//   - empty                → ""
//   - an existing asset    → passed through unchanged (a "/s3/..." path,
//     an asset URL on our own storage host, or — under --no-fetch /
//     --asset — any value the caller vouched for)
//   - a remote http(s) URL → downloaded client-side (yt-dlp for
//     streaming/social, plain HTTP for direct media links), uploaded as a
//     pipe2 asset; only the asset URL is returned
//   - a local path         → uploaded via the CLI's asset-upload machinery
//
// Re-used resolutions are cached for the lifetime of the Context so the
// same source passed for two inputs only fetches/uploads once per run.
//
// Unlike ResolveAssetURL, this returns an error rather than panicking: a
// fetch/upload failure (a 403, a non-media page, a missing yt-dlp) is an
// expected user-facing condition that the recipe must surface as a clean
// message — never a stack trace. Requires a Client wired into the Context
// for the fetch/upload paths; existing-asset references resolve without one.
func (c *Context) ResolveSourceURL(name string) (string, error) {
	raw := c.Inputs.String(name)

	switch classifySource(raw, c.storageURL, c.noFetch) {
	case sourceEmpty:
		return "", nil
	case sourceExistingAsset:
		// Already an asset reference the backend resolves directly — a
		// "/s3/<bucket>/<key>" path, an asset URL on our own storage host,
		// or anything under --no-fetch. Re-uploading it would stat()-fail.
		return raw, nil
	case sourceRemote:
		return c.resolveRemote(name, raw)
	default: // sourceLocalPath
		return c.uploadCached(raw)
	}
}

// resolveRemote downloads a third-party URL client-side and uploads the
// bytes as an asset, returning the asset URL. The temp download is always
// cleaned up — on success and on failure.
func (c *Context) resolveRemote(name, raw string) (string, error) {
	if cached, ok := c.uploadCache[raw]; ok {
		return cached, nil
	}
	// Dry-run: don't touch the network. Return a placeholder; every
	// downstream step is dry too, so it never reaches a real API.
	if c.dryRun {
		stub := "dry://fetch/" + raw
		c.uploadCache[raw] = stub
		c.Logf("◌ would fetch + upload %s", raw)
		return stub, nil
	}
	if c.client == nil {
		// A nil client here is a programmer/test-setup bug, not a user
		// condition — panic rather than dress it up as a fetch error.
		panic(fmt.Sprintf("Context.ResolveSourceURL(%q): remote URL %q passed but no Client is wired (test context?)", name, raw))
	}

	fetch := c.fetcher
	if fetch == nil {
		fetch = fetchRemoteSource
	}
	path, cleanup, err := fetch(c.ctx, raw, c.Logf)
	// Always clean up the temp download — on success (after upload) and on
	// failure. cleanup is guaranteed non-nil by the fetcher contract.
	defer cleanup()
	if err != nil {
		// fetchRemoteSource already wraps in *SourceFetchError ("source
		// fetch failed: ..."). Surface it as the recipe's error — never a
		// downstream ffprobe failure, never a stack trace.
		return "", err
	}

	c.Logf("uploading fetched source as a pipe2 asset")
	url, err := c.client.UploadAsset(c.ctx, path)
	if err != nil {
		return "", fmt.Errorf("uploading fetched source %s: %w", raw, err)
	}
	c.uploadCache[raw] = url
	return url, nil
}

// uploadCached uploads a local file (memoized per Context) and returns its
// asset URL, or an error if the upload fails.
func (c *Context) uploadCached(raw string) (string, error) {
	if cached, ok := c.uploadCache[raw]; ok {
		return cached, nil
	}
	if c.dryRun {
		stub := "dry://upload" + raw
		c.uploadCache[raw] = stub
		c.Logf("◌ would upload %s", raw)
		return stub, nil
	}
	c.Logf("uploading %s", raw)
	if c.client == nil {
		panic(fmt.Sprintf("Context.resolve: local path %q passed but no Client is wired (test context?)", raw))
	}
	url, err := c.client.UploadAsset(c.ctx, raw)
	if err != nil {
		return "", fmt.Errorf("upload %s: %w", raw, err)
	}
	c.uploadCache[raw] = url
	return url, nil
}

// ResolveStorageURL turns a stored asset reference into a fetchable
// URL. Absolute http(s) URLs pass through unchanged. A "/s3/..." path
// is the platform's storage convention — the leading "/s3" is stripped
// and the remainder joined onto storageBase:
//
//	ResolveStorageURL("/s3/pipe2-ai/generated/x.mp4", "http://h:8333")
//	  → "http://h:8333/pipe2-ai/generated/x.mp4"
//
// An empty storageBase leaves a relative path untouched (callers then
// fail loudly on the unfetchable URL rather than guessing a host).
func ResolveStorageURL(raw, storageBase string) string {
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	if storageBase == "" {
		return raw
	}
	p := strings.TrimPrefix(raw, "/s3")
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return strings.TrimRight(storageBase, "/") + p
}

// DownloadFile GETs url into path atomically (write-tmp-then-rename),
// creating parent directories. Returns bytes written. The single
// canonical downloader — Context.Capture and `pipe2 recipe download`
// both call it so the fetch/atomicity behaviour stays identical.
func DownloadFile(ctx context.Context, url, path string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, resp.Body)
	if err != nil {
		f.Close()
		os.Remove(tmp)
		return 0, err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return 0, err
	}
	return n, os.Rename(tmp, path)
}
