package cli

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	pipe2 "github.com/pipe2-ai/sdk-go"
)

// listAssetsVars omits $where so the schema default `{}` applies. Same
// rationale as listPipelineRunsVars in runs.go — the generated
// `*Assets_bool_exp` struct has no omitempty tags and marshals as
// `{..., "pipeline_run": null, ...}`, which Hasura rejects.
type listAssetsVars struct {
	Limit  *int `json:"limit,omitempty"`
	Offset *int `json:"offset,omitempty"`
}

func newAssetsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "assets",
		Aliases: []string{"asset"},
		Short:   "Upload, inspect, and delete assets",
	}
	c.AddCommand(newAssetsListCmd(), newAssetsDeleteCmd(), newAssetsUploadCmd())
	return c
}

// Size limits mirror packages/api/internal/actions/multipart_upload.go.
// Kept in sync by hand — the action enforces nothing on its end (presigned
// PUT URLs don't carry Content-Length policy), so the CLI must guard.
//
// Files at or below singlePutThreshold use the single-PUT path (one
// request_upload + one PUT + one create_asset); larger files use the
// multipart flow (request_multipart_upload → N parallel PUTs → complete).
const (
	maxImageUploadSize int64 = 10 << 20 // 10 MiB — multipart is not useful at image sizes
	maxVideoUploadSize int64 = 5 << 30  // 5 GiB
	maxAudioUploadSize int64 = 1 << 30  // 1 GiB

	// singlePutThreshold is the cutoff for choosing single-PUT vs multipart.
	// Below this we save a roundtrip (one fewer presigned URL mint, no
	// per-part bookkeeping). Above this we get parallelism + resumable
	// failure recovery.
	singlePutThreshold int64 = 25 << 20 // 25 MiB

	// Defaults for the multipart path. The minimum part size is set by S3
	// (5 MiB except for the last part). 32 MiB keeps the part count low
	// for big files (5 GiB / 32 MiB = 160 parts, well below S3's 10000 cap).
	defaultMultipartPartSize int64 = 32 << 20 // 32 MiB
	defaultMultipartParallel       = 4

	// Generous timeout per part — 32 MiB on a slow connection can be a
	// minute or two. The presigned URL itself is valid for 1 hour.
	partUploadTimeout = 10 * time.Minute
)

// detectContentType picks a content type for an upload. Order:
//  1. explicit --content-type flag
//  2. mime.TypeByExtension on the filename (fast, no I/O)
//  3. http.DetectContentType on the first 512 bytes (fallback for unknown extensions)
//
// Returns ("", error) if nothing matched image/*, video/*, or audio/* — the
// backend rejects anything else, so failing here gives a clearer error.
func detectContentType(path, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext != "" {
		if ct := mime.TypeByExtension(ext); ct != "" {
			// mime sometimes appends `; charset=utf-8` for text — strip params.
			if i := strings.Index(ct, ";"); i >= 0 {
				ct = ct[:i]
			}
			return strings.TrimSpace(ct), nil
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	ct := http.DetectContentType(buf[:n])
	return ct, nil
}

func mediaCategory(contentType string) (string, int64, error) {
	switch {
	case strings.HasPrefix(contentType, "image/"):
		return "image", maxImageUploadSize, nil
	case strings.HasPrefix(contentType, "video/"):
		return "video", maxVideoUploadSize, nil
	case strings.HasPrefix(contentType, "audio/"):
		return "audio", maxAudioUploadSize, nil
	default:
		return "", 0, fmt.Errorf("unsupported content type %q (must be image/*, video/*, or audio/*)", contentType)
	}
}

// putToPresignedURL streams the file body to the presigned URL. We avoid
// loading the file into memory — the 50 MiB video cap is comfortable on
// disk but adds up if many uploads happen concurrently.
func putToPresignedURL(ctx context.Context, presignedURL, contentType string, file *os.File, size int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, presignedURL, file)
	if err != nil {
		return fmt.Errorf("build PUT request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.ContentLength = size

	// Generous timeout — 50 MiB on a slow connection can be a couple of
	// minutes. The presigned URL itself is valid for 5 minutes.
	httpClient := &http.Client{Timeout: 5 * time.Minute}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("PUT to S3: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("S3 returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func newAssetsUploadCmd() *cobra.Command {
	var (
		contentTypeFlag string
		tagsFlag        []string
		partSizeFlag    int64
		parallelFlag    int
	)
	c := &cobra.Command{
		Use:   "upload <file>",
		Short: "Upload a local file as an asset (image/video/audio)",
		Long: `Upload a local file and register it as an asset in your library.

The asset is stored in Pipe2.ai's S3 bucket and gets a public URL you can
pass to any pipeline (e.g. video-trim, transcription, captions) via either
the asset's id or its url field.

Files at or below 25 MiB use a single PUT. Larger files automatically use
S3 multipart with parallel chunked PUTs (--parallel chunks at a time, each
--part-size MiB). On Ctrl-C the in-progress upload is aborted cleanly.

Size limits: 10 MiB images, 5 GiB videos, 1 GiB audio.

Examples:
  pipe2 assets upload ./interview.mp4
  pipe2 assets upload ./voiceover.wav --tags podcast,episode-42
  pipe2 assets upload ./long-podcast.mp4 --parallel 8 --part-size 64
  pipe2 assets upload ./photo --content-type image/jpeg`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]

			contentType, err := detectContentType(path, contentTypeFlag)
			if err != nil {
				return &ExitError{Code: ExitUsage, Err: err}
			}
			category, maxSize, err := mediaCategory(contentType)
			if err != nil {
				return &ExitError{Code: ExitUsage, Err: err}
			}

			info, err := os.Stat(path)
			if err != nil {
				return &ExitError{Code: ExitUsage, Err: fmt.Errorf("open %s: %w", path, err)}
			}
			if info.IsDir() {
				return &ExitError{Code: ExitUsage, Err: fmt.Errorf("%s is a directory", path)}
			}
			if info.Size() == 0 {
				return &ExitError{Code: ExitUsage, Err: fmt.Errorf("%s is empty", path)}
			}
			if info.Size() > maxSize {
				return &ExitError{Code: ExitUsage, Err: fmt.Errorf(
					"%s is %d bytes — exceeds %s limit of %d bytes",
					path, info.Size(), category, maxSize,
				)}
			}

			client, err := MustClient()
			if err != nil {
				return err
			}

			// Auto-route based on file size. Single PUT is faster and
			// simpler for small files; multipart unlocks GiB-scale
			// uploads with parallelism + resumable cleanup.
			if info.Size() <= singlePutThreshold {
				return runSinglePutUpload(cmd.Context(), client, path, contentType, category, info.Size(), tagsFlag)
			}

			partSize := partSizeFlag * (1 << 20) // flag is in MiB
			if partSize <= 0 {
				partSize = defaultMultipartPartSize
			}
			parallel := parallelFlag
			if parallel <= 0 {
				parallel = defaultMultipartParallel
			}
			return runMultipartUpload(cmd.Context(), client, path, contentType, info.Size(), tagsFlag, partSize, parallel)
		},
	}
	c.Flags().StringVar(&contentTypeFlag, "content-type", "", "explicit MIME type (default: detected from extension)")
	c.Flags().StringSliceVar(&tagsFlag, "tags", nil, "comma-separated tags to attach to the asset")
	c.Flags().Int64Var(&partSizeFlag, "part-size", 0, "multipart chunk size in MiB (default 32, min 5)")
	c.Flags().IntVar(&parallelFlag, "parallel", 0, "concurrent part uploads in multipart mode (default 4)")
	return c
}

// runSinglePutUpload handles the small-file path: one presigned URL, one
// PUT, one create_asset. Same flow as the original implementation, factored
// out so the cobra RunE can route between this and runMultipartUpload.
func runSinglePutUpload(ctx context.Context, client graphql.Client, path, contentType, category string, size int64, tags []string) error {
	Status("requesting upload URL for %s (%s, %d bytes)...", filepath.Base(path), contentType, size)
	presign, err := pipe2.RequestUpload(ctx, client, filepath.Base(path), contentType)
	if err != nil {
		return classifyAPIError(err)
	}

	Status("uploading to S3...")
	f, err := os.Open(path)
	if err != nil {
		return &ExitError{Code: ExitGeneric, Err: err}
	}
	defer f.Close()
	if err := putToPresignedURL(ctx, presign.Request_upload.Upload_url, contentType, f, size); err != nil {
		return &ExitError{Code: ExitGeneric, Err: err}
	}

	Status("registering asset...")
	created, err := pipe2.CreateAsset(ctx, client, presign.Request_upload.Key, tags)
	if err != nil {
		return classifyAPIError(err)
	}
	_ = category // server now derives type from S3 content_type; flag preserved for backward-compatible UX
	return Out(created.Create_asset)
}

func newAssetsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List your assets",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := MustClient()
			if err != nil {
				return err
			}
			// Call the operation directly so we can omit $where and let
			// the schema default `{}` apply. See listAssetsVars above.
			req := &graphql.Request{
				OpName:    "GetUserAssets",
				Query:     pipe2.GetUserAssets_Operation,
				Variables: listAssetsVars{},
			}
			data := &pipe2.GetUserAssetsResponse{}
			if err := client.MakeRequest(cmd.Context(), req, &graphql.Response{Data: data}); err != nil {
				return classifyAPIError(err)
			}
			return Out(data.Assets)
		},
	}
}

func newAssetsDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <asset-id>",
		Short: "Delete an asset (DB row + S3 object)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := MustClient()
			if err != nil {
				return err
			}
			Status("deleting asset %s...", args[0])
			resp, err := pipe2.DeleteAssetAction(cmd.Context(), client, args[0])
			if err != nil {
				return classifyAPIError(err)
			}
			return Out(resp.Delete_asset)
		},
	}
}
