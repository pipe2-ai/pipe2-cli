package cookbook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	ytdlp "github.com/lrstanley/go-ytdlp"
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
// Two paths, with a safety net:
//   - A direct media link (URL path ends in a media extension) is streamed
//     over plain HTTP — the fast path.
//   - Everything else goes to the yt-dlp extractor (YouTube, Vimeo, TikTok,
//     …), which can merge separate video+audio streams via ffmpeg.
//
// The routing is a heuristic, not a contract: if the "direct" fast path
// turns out to serve a non-media body (an HTML consent/error page behind a
// media-looking URL), we fall back to the extractor rather than letting the
// misroute resurface downstream as `ffprobe: Invalid data`. A genuine
// transport failure (a 403, a network error) is NOT retried via the
// extractor — same egress, same result — and surfaces as a clean error.
// Any failure is wrapped in *SourceFetchError.
func fetchRemoteSource(ctx context.Context, rawURL string, progress progressFunc) (path string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "pipe2-fetch-")
	if err != nil {
		return "", func() {}, &SourceFetchError{URL: rawURL, Err: fmt.Errorf("create temp dir: %w", err)}
	}
	cleanup = func() { _ = os.RemoveAll(dir) }

	if isDirectMediaURL(rawURL) {
		path, err = httpDownloadMedia(ctx, rawURL, dir, progress)
		// Misroute recovery: the URL looked direct but served non-media.
		// The non-media check happens before any file is written, so dir is
		// clean for the extractor to write into.
		var nm *nonMediaError
		if errors.As(err, &nm) {
			progress.log("direct link served non-media content (%s); falling back to the yt-dlp extractor", nm.contentType)
			path, err = ytdlpDownload(ctx, rawURL, dir, progress)
		}
	} else {
		path, err = ytdlpDownload(ctx, rawURL, dir, progress)
	}
	if err != nil {
		cleanup()
		return "", func() {}, &SourceFetchError{URL: rawURL, Err: err}
	}
	return path, cleanup, nil
}

// nonMediaError marks a "direct" fetch that returned a successful HTTP
// response whose body is not media (typically an HTML consent/error page).
// fetchRemoteSource treats it as a misroute and retries via the extractor,
// so it must be distinguishable from a hard transport failure.
type nonMediaError struct {
	url         string
	contentType string
}

func (e *nonMediaError) Error() string {
	return fmt.Sprintf("%s returned non-media content (%s) — looks like a web page, not a video/audio file", e.url, e.contentType)
}

// browserUA is sent on direct-media GETs. Some CDNs 403 a default Go
// User-Agent (the worker's egress saw exactly this); a normal browser UA
// gets the public object.
const browserUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/124.0 Safari/537.36"

// httpDownloadMedia GETs a direct media URL into dir and returns the saved
// path. It fails loudly — and correctly — on the cases that used to reach
// ffprobe as garbage:
//   - a non-2xx status → a hard transport error (no extractor retry)
//   - a 2xx whose body is not media → a *nonMediaError, which the caller
//     recovers by falling back to the extractor
//
// A blank or application/octet-stream content-type is tolerated (plenty of
// valid CDNs serve media that way), but the first bytes are sniffed in that
// case so an HTML page mislabeled as octet-stream is still caught. Redirects
// are followed by the stdlib client.
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

	ct := normalizeContentType(resp.Header.Get("Content-Type"))
	switch classifyContentType(ct) {
	case ctMedia:
		// trusted media header — stream as-is
	case ctNonMedia:
		return "", &nonMediaError{url: rawURL, contentType: ct}
	default: // ctAmbiguous (blank / octet-stream) — sniff the leading bytes
		head, herr := peek(resp.Body, 512)
		if herr != nil {
			return "", fmt.Errorf("read body: %w", herr)
		}
		if sniffedNonMedia(head) {
			label := ct
			if label == "" {
				label = "blank content-type"
			}
			return "", &nonMediaError{url: rawURL, contentType: label}
		}
		// Re-attach the sniffed head in front of the rest of the body.
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(head), resp.Body), resp.Body}
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
		_ = os.Remove(dest) // leave dir clean for a potential extractor fallback
		return "", fmt.Errorf("download body: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(dest)
		return "", err
	}
	return dest, nil
}

// normalizeContentType lower-cases a Content-Type header and strips any
// "; charset=..." parameters.
func normalizeContentType(raw string) string {
	ct := strings.ToLower(strings.TrimSpace(raw))
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	return ct
}

type contentClass int

const (
	ctAmbiguous contentClass = iota // blank / octet-stream — needs sniffing
	ctMedia                         // explicit video/audio/image
	ctNonMedia                      // explicit text/html, application/json, …
)

func classifyContentType(ct string) contentClass {
	switch {
	case ct == "", ct == "application/octet-stream", ct == "binary/octet-stream":
		return ctAmbiguous
	case strings.HasPrefix(ct, "video/"),
		strings.HasPrefix(ct, "audio/"),
		strings.HasPrefix(ct, "image/"):
		return ctMedia
	default:
		return ctNonMedia
	}
}

// sniffedNonMedia reports whether a body's leading bytes look like a web
// page / text rather than media. Used only when the declared content-type
// is ambiguous (blank / octet-stream).
func sniffedNonMedia(head []byte) bool {
	sniffed := normalizeContentType(http.DetectContentType(head))
	return strings.HasPrefix(sniffed, "text/")
}

// peek reads up to n bytes from r, tolerating a short read at EOF.
func peek(r io.Reader, n int) ([]byte, error) {
	buf := make([]byte, n)
	read, err := io.ReadFull(r, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	return buf[:read], nil
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

// ytdlpDownload resolves a streaming/social URL to a local file with the
// yt-dlp extractor, via github.com/lrstanley/go-ytdlp. The yt-dlp + ffmpeg
// binaries are bootstrapped on first use (checksum-verified download into
// go-ytdlp's cache); a bootstrap failure surfaces as an actionable error,
// never a stack trace.
func ytdlpDownload(ctx context.Context, rawURL, dir string, progress progressFunc) (string, error) {
	if err := ensureYtdlp(ctx, progress); err != nil {
		return "", err
	}

	progress.log("resolving %s with yt-dlp", rawURL)
	cmd := ytdlp.New().
		NoPlaylist(). // a single-video URL must not drag in a whole playlist
		NoColors().
		Paths(dir).               // download into our temp dir
		Output("source.%(ext)s"). // yt-dlp picks the real container
		ProgressFunc(time.Second, func(u ytdlp.ProgressUpdate) {
			if u.TotalBytes > 0 {
				progress.log("  yt-dlp %s (%.1f / %.1f MiB)", u.PercentString(),
					float64(u.DownloadedBytes)/(1<<20), float64(u.TotalBytes)/(1<<20))
			} else {
				progress.log("  yt-dlp %s", u.Status)
			}
		}).
		StderrFunc(func(line string) {
			if s := strings.TrimSpace(line); s != "" {
				progress.log("  yt-dlp: %s", s)
			}
		})
	if _, err := cmd.Run(ctx, rawURL); err != nil {
		return "", fmt.Errorf("yt-dlp could not resolve %s: %w (the URL may be private, geo-blocked, age-restricted, or need cookies)", rawURL, err)
	}

	// yt-dlp chose the extension; find the single file it produced.
	produced, err := singleFileIn(dir)
	if err != nil {
		return "", fmt.Errorf("yt-dlp reported success but %w", err)
	}
	return produced, nil
}

// ytdlpBootstrap memoizes the one-time install of yt-dlp + ffmpeg so
// concurrent clip fan-out doesn't race (and doesn't repeat the log line).
var (
	ytdlpBootstrapOnce sync.Once
	ytdlpBootstrapErr  error
)

// ensureYtdlp makes sure yt-dlp + ffmpeg/ffprobe are available, downloading
// them (checksum-verified, into go-ytdlp's cache) on first use. The pinned
// version is whatever the go-ytdlp module ships (ytdlp.Version); bump the
// dependency to move it. Env opt-outs:
//
//	PIPE2_YTDLP_SYSTEM=1       use a yt-dlp already on PATH, any version
//	                           (bring-your-own / install-latest path)
//	PIPE2_YTDLP_NO_DOWNLOAD=1  never download; require a system install
func ensureYtdlp(ctx context.Context, progress progressFunc) error {
	ytdlpBootstrapOnce.Do(func() {
		cacheDir, _ := ytdlp.GetCacheDir()
		progress.log("ensuring yt-dlp %s + ffmpeg are available (first run downloads, checksum-verified, into %s)", ytdlp.Version, cacheDir)

		if _, err := ytdlp.Install(ctx, ytdlpInstallOptions()); err != nil {
			ytdlpBootstrapErr = bootstrapError("yt-dlp", cacheDir, err)
			return
		}
		// ffmpeg + ffprobe are NOT optional: YouTube's best quality is
		// separate video+audio streams that yt-dlp merges with ffmpeg. With
		// yt-dlp present but ffmpeg absent, yt-dlp silently falls back to a
		// progressive (≤720p) format — so we provision them here and fail
		// loudly if we can't, rather than let quality silently degrade.
		// go-ytdlp adds its cache dir to the child PATH so yt-dlp finds them.
		ffOpts := ytdlpFFmpegOptions()
		if _, err := ytdlp.InstallFFmpeg(ctx, ffOpts); err != nil {
			ytdlpBootstrapErr = bootstrapError("ffmpeg", cacheDir, err)
			return
		}
		if _, err := ytdlp.InstallFFprobe(ctx, ffOpts); err != nil {
			ytdlpBootstrapErr = bootstrapError("ffprobe", cacheDir, err)
			return
		}
	})
	return ytdlpBootstrapErr
}

func ytdlpInstallOptions() *ytdlp.InstallOptions {
	opts := &ytdlp.InstallOptions{}
	if os.Getenv("PIPE2_YTDLP_NO_DOWNLOAD") != "" {
		// Air-gapped / sandboxed: require a system yt-dlp, never reach out.
		opts.DisableDownload = true
	}
	if os.Getenv("PIPE2_YTDLP_SYSTEM") != "" {
		// Use whatever yt-dlp is on PATH regardless of version — the
		// "bring my own / install-latest" path.
		opts.AllowVersionMismatch = true
	}
	return opts
}

// ytdlpFFmpegOptions mirrors PIPE2_YTDLP_NO_DOWNLOAD for the ffmpeg/ffprobe
// install so the offline path is consistent: a NO_DOWNLOAD run requires a
// system ffmpeg and fails loudly if it's missing, instead of silently
// downloading it (or silently downgrading quality).
func ytdlpFFmpegOptions() *ytdlp.InstallFFmpegOptions {
	opts := &ytdlp.InstallFFmpegOptions{}
	if os.Getenv("PIPE2_YTDLP_NO_DOWNLOAD") != "" {
		opts.DisableDownload = true
	}
	return opts
}

// bootstrapError turns a go-ytdlp install failure into an actionable
// message: where the cache lives, why it might have failed (offline,
// sandboxed, checksum mismatch), and the env opt-outs.
func bootstrapError(tool, cacheDir string, err error) error {
	return fmt.Errorf(
		"could not bootstrap %s (auto-installed, checksum-verified, into %s): %w\n"+
			"  This first-run download needs network access to GitHub. If this host is offline or sandboxed, "+
			"install yt-dlp + ffmpeg yourself and set PIPE2_YTDLP_SYSTEM=1 to use the ones on PATH "+
			"(or PIPE2_YTDLP_NO_DOWNLOAD=1 to require a system install and skip the download). "+
			"A checksum mismatch usually means a corrupted or interrupted download — retry, or delete the cache dir above and try again",
		tool, cacheDir, err)
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
