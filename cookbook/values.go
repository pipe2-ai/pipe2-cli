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

// AssetURL returns the value for name as a URL ready to pass to a
// pipeline. If the user supplied a local path (anything that doesn't
// start with http(s)://), the runtime uploads it via the CLI's
// existing asset-upload machinery and returns the resulting URL.
//
// Re-used uploads are cached for the lifetime of the Context so two
// inputs pointing at the same local file only upload once per run.
//
// Requires a Client wired into the Context — TestContexts that
// don't set one will panic on a local path. URLs always pass through.
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
	if cached, ok := c.uploadCache[raw]; ok {
		return cached
	}
	// Dry-run: don't upload, return a synthetic placeholder. Recipes
	// pass this string downstream — all subsequent steps are also dry,
	// so the placeholder never reaches a real API.
	if c.dryRun {
		stub := "dry://upload" + raw
		c.uploadCache[raw] = stub
		c.Logf("◌ would upload %s", raw)
		return stub
	}
	c.Logf("uploading %s", raw)
	if c.client == nil {
		panic(fmt.Sprintf("Context.ResolveAssetURL(%q): local path %q passed but no Client is wired (test context?)", name, raw))
	}
	url, err := c.client.UploadAsset(c.ctx, raw)
	if err != nil {
		panic(fmt.Sprintf("Context.ResolveAssetURL(%q): upload %q failed: %v", name, raw, err))
	}
	c.uploadCache[raw] = url
	return url
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
