package cookbook

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Source ingestion lives here. The platform never fetches external URLs
// server-side — the worker's datacenter IP is 403'd by YouTube and a
// naive GET of a watch page hands ffprobe an HTML body ("Invalid data").
// So the CLI, which runs on the user's machine/runtime (usually a
// residential IP), resolves a remote --input client-side: download it
// (yt-dlp for streaming/social, plain HTTP for direct media links),
// upload the bytes as a pipe2 asset, and pass only the asset URL onward.
//
// The guardrail: a non-2xx response or a non-media body fails loudly as
// "source fetch failed", never as a downstream ffprobe error.

// sourceKind classifies a raw --input value so ResolveSourceURL can route
// it. The classification is pure (no I/O) and unit-tested in fetch_test.go.
type sourceKind int

const (
	// sourceEmpty is the zero/blank input — resolves to "".
	sourceEmpty sourceKind = iota
	// sourceExistingAsset is already an asset reference the backend can
	// resolve directly (a "/s3/..." path, an asset URL on our own storage
	// host, or — under --no-fetch — any value the caller vouches for).
	// Passed through verbatim: no download, no upload.
	sourceExistingAsset
	// sourceLocalPath is a path on the local filesystem — uploaded as an
	// asset via the existing auto-upload machinery.
	sourceLocalPath
	// sourceRemote is a third-party http(s) URL — downloaded client-side
	// then uploaded as an asset.
	sourceRemote
)

// classifySource decides how a raw --input value should be resolved.
//
// storageBase is the configured asset-storage base (Config.EffectiveStorageURL);
// an http(s) input on that same host is one of our own already-uploaded
// assets and is passed through rather than re-downloaded. noFetch is the
// --no-fetch escape hatch: when set, every value is treated as an asset
// reference the caller has already uploaded, so nothing is fetched or
// uploaded.
func classifySource(raw, storageBase string, noFetch bool) sourceKind {
	if raw == "" {
		return sourceEmpty
	}
	// --no-fetch: the caller asserts every input is already an asset
	// reference (URL, "/s3/..." path, or asset id). Pass through verbatim.
	if noFetch {
		return sourceExistingAsset
	}
	// Storage-relative asset path written by `pipe2 assets upload` and every
	// pipeline activity. The backend resolves it directly.
	if strings.HasPrefix(raw, "/s3/") {
		return sourceExistingAsset
	}
	if isHTTPURL(raw) {
		// An asset URL on our own storage host is already uploaded — reuse
		// it instead of round-tripping a download + re-upload.
		if storageBase != "" && sameHost(raw, storageBase) {
			return sourceExistingAsset
		}
		return sourceRemote
	}
	// Anything else is a path on the local filesystem.
	return sourceLocalPath
}

func isHTTPURL(raw string) bool {
	return strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://")
}

// sameHost reports whether two URLs share a (case-insensitive) host. A
// parse failure on either side is treated as "not the same host" so a
// malformed value never masquerades as an own-storage asset.
func sameHost(a, b string) bool {
	ua, err := url.Parse(a)
	if err != nil {
		return false
	}
	ub, err := url.Parse(b)
	if err != nil {
		return false
	}
	return ua.Hostname() != "" && strings.EqualFold(ua.Hostname(), ub.Hostname())
}

// mediaExtensions are the container/codec extensions we treat as a direct
// media link (download over plain HTTP). Anything without one of these is
// assumed to be a streaming/social page that needs yt-dlp to resolve.
var mediaExtensions = map[string]bool{
	".mp4": true, ".m4v": true, ".mov": true, ".webm": true, ".mkv": true,
	".avi": true, ".flv": true, ".mpg": true, ".mpeg": true, ".ogv": true,
	".mp3": true, ".m4a": true, ".wav": true, ".aac": true, ".flac": true,
	".ogg": true, ".opus": true,
}

// isDirectMediaURL reports whether the URL path ends in a known media
// extension — the signal we use to pick plain HTTP over yt-dlp. Query
// strings are ignored (a presigned URL keeps its real extension in the
// path).
func isDirectMediaURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return mediaExtensions[strings.ToLower(filepath.Ext(u.Path))]
}

// SourceFetchError is the "fail loudly" guardrail in typed form. Every
// remote-fetch failure surfaces as one of these with a message that
// starts "source fetch failed", so the user sees the real cause (a 403,
// an HTML page, a missing yt-dlp) instead of a downstream
// "ffprobe: Invalid data". Recipes can errors.As into it if they want to
// react to a fetch failure specifically.
type SourceFetchError struct {
	URL string
	Err error
}

func (e *SourceFetchError) Error() string {
	return fmt.Sprintf("source fetch failed: %s: %v", e.URL, e.Err)
}
func (e *SourceFetchError) Unwrap() error { return e.Err }

// progressFunc receives human-readable progress lines (stderr-bound by the
// caller). Nil is tolerated.
type progressFunc func(format string, args ...any)

func (p progressFunc) log(format string, args ...any) {
	if p != nil {
		p(format, args...)
	}
}

// fetchRemoteSource downloads rawURL to a freshly-created temp directory
// and returns the path to the downloaded file plus a cleanup func that
// removes the temp directory. The cleanup func is ALWAYS returned non-nil
// and is safe to call even on error — callers defer it unconditionally so
// the temp bytes never leak, on success or failure.
//
// Direct media links (a URL whose path ends in a media extension) are
// fetched over plain HTTP; everything else is handed to yt-dlp, which
// resolves streaming/social pages (YouTube, Vimeo, TikTok, …) to an actual
// stream. Any failure is wrapped in *SourceFetchError.
func fetchRemoteSource(ctx context.Context, rawURL string, progress progressFunc) (path string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "pipe2-fetch-")
	if err != nil {
		return "", func() {}, &SourceFetchError{URL: rawURL, Err: fmt.Errorf("create temp dir: %w", err)}
	}
	cleanup = func() { _ = os.RemoveAll(dir) }

	if isDirectMediaURL(rawURL) {
		path, err = httpDownloadMedia(ctx, rawURL, dir, progress)
	} else {
		path, err = ytdlpDownload(ctx, rawURL, dir, progress)
	}
	if err != nil {
		cleanup()
		return "", func() {}, &SourceFetchError{URL: rawURL, Err: err}
	}
	return path, cleanup, nil
}

// browserUA is sent on direct-media GETs. Some CDNs 403 a default Go
// User-Agent (the worker's egress saw exactly this); a normal browser UA
// gets the public object.
const browserUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/124.0 Safari/537.36"

// httpDownloadMedia GETs a direct media URL into dir and returns the saved
// path. It fails loudly — and correctly — on the two cases that used to
// reach ffprobe as garbage: a non-2xx status, and a 200 whose body is not
// media (an HTML consent/error page). Redirects are followed by the
// stdlib client.
func httpDownloadMedia(ctx context.Context, rawURL, dir string, progress progressFunc) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "*/*")

	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("%s returned HTTP %d", rawURL, resp.StatusCode)
	}

	// Content-type guardrail: a 200 with text/html is the consent/error
	// page trap. Reject anything that isn't media so ffprobe never sees a
	// non-media body. application/octet-stream is allowed — plenty of CDNs
	// serve media that way and the path extension already told us it's media.
	ct := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	if !isMediaContentType(ct) {
		return "", fmt.Errorf("%s returned non-media content-type %q (expected a video/audio file, got what looks like a web page)", rawURL, ct)
	}

	// Name the temp file after the URL's basename so the uploader can infer
	// a content type from the extension.
	name := filepath.Base(strings.SplitN(rawURL, "?", 2)[0])
	if name == "" || name == "." || name == "/" {
		name = "source" + extForContentType(ct)
	}
	dest := filepath.Join(dir, name)

	f, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	progress.log("downloading %s", rawURL)
	w := &progressWriter{total: resp.ContentLength, progress: progress, label: "download"}
	if _, err := io.Copy(io.MultiWriter(f, w), resp.Body); err != nil {
		f.Close()
		return "", fmt.Errorf("download body: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return dest, nil
}

func isMediaContentType(ct string) bool {
	switch {
	case ct == "", ct == "application/octet-stream", ct == "binary/octet-stream":
		// Unknown/opaque — the path extension already classified this as
		// media, so accept it rather than reject a legitimately-served file.
		return true
	case strings.HasPrefix(ct, "video/"),
		strings.HasPrefix(ct, "audio/"),
		strings.HasPrefix(ct, "image/"):
		return true
	default:
		return false
	}
}

// extForContentType maps a media content type back to a file extension for
// the rare case where the URL path carries no usable basename.
func extForContentType(ct string) string {
	switch ct {
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "video/quicktime":
		return ".mov"
	case "audio/mpeg":
		return ".mp3"
	case "audio/mp4":
		return ".m4a"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	default:
		return ".bin"
	}
}

// ytdlpDownload resolves a streaming/social URL to a local file via
// yt-dlp. yt-dlp is an external dependency: when it isn't on PATH we emit
// an actionable error (how to install it, or how to side-step it) rather
// than a stack trace.
func ytdlpDownload(ctx context.Context, rawURL, dir string, progress progressFunc) (string, error) {
	bin, err := exec.LookPath("yt-dlp")
	if err != nil {
		return "", fmt.Errorf(
			"this looks like a streaming/social URL, which needs yt-dlp to resolve, but yt-dlp was not found on PATH.\n" +
				"  Install it (https://github.com/yt-dlp/yt-dlp#installation), e.g. `pipx install yt-dlp` or `brew install yt-dlp`,\n" +
				"  or pass a direct media file URL / a local file / an already-uploaded `--asset <id>` instead")
	}

	// Output template inside our temp dir; %(ext)s lets yt-dlp pick the
	// real container. --no-playlist keeps a single-video URL from pulling a
	// whole playlist. --newline makes progress one-line-per-update so it
	// streams cleanly to stderr.
	outTmpl := filepath.Join(dir, "source.%(ext)s")
	args := []string{
		"--no-playlist",
		"--no-progress",
		"--newline",
		"--no-color",
		"-o", outTmpl,
		rawURL,
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	// yt-dlp diagnostics go to the user's stderr (progress / status), never
	// to --json stdout.
	cmd.Stdout = progressSink{progress: progress, label: "yt-dlp"}
	cmd.Stderr = progressSink{progress: progress, label: "yt-dlp"}
	progress.log("resolving %s with yt-dlp", rawURL)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("yt-dlp failed for %s: %w (see the yt-dlp output above; the URL may be private, geo-blocked, or need cookies)", rawURL, err)
	}

	// yt-dlp chose the extension; find the single file it produced.
	produced, err := singleFileIn(dir)
	if err != nil {
		return "", fmt.Errorf("yt-dlp reported success but %w", err)
	}
	return produced, nil
}

// singleFileIn returns the sole regular file in dir. yt-dlp writes exactly
// one output file for a single-video download; more or fewer means
// something unexpected happened (a playlist slipped through, or nothing
// downloaded).
func singleFileIn(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var files []string
	for _, e := range entries {
		if e.Type().IsRegular() {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	switch len(files) {
	case 0:
		return "", errors.New("no output file was written")
	case 1:
		return files[0], nil
	default:
		return "", fmt.Errorf("wrote %d files (expected exactly one)", len(files))
	}
}

// progressWriter logs download progress every few MiB so a large fetch
// shows life on stderr without flooding it.
type progressWriter struct {
	total    int64 // -1 when the server sent no Content-Length
	written  int64
	lastLog  int64
	progress progressFunc
	label    string
}

func (w *progressWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.written += int64(n)
	const step = 8 << 20 // log every 8 MiB
	if w.written-w.lastLog >= step {
		w.lastLog = w.written
		if w.total > 0 {
			pct := float64(w.written) / float64(w.total) * 100
			w.progress.log("  %s %.0f%% (%.1f / %.1f MiB)", w.label, pct,
				float64(w.written)/(1<<20), float64(w.total)/(1<<20))
		} else {
			w.progress.log("  %s %.1f MiB", w.label, float64(w.written)/(1<<20))
		}
	}
	return n, nil
}

// progressSink adapts a child process's output stream to the progress
// logger, one line at a time.
type progressSink struct {
	progress progressFunc
	label    string
}

func (s progressSink) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			s.progress.log("  %s: %s", s.label, line)
		}
	}
	return len(p), nil
}
