package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Khan/genqlient/graphql"
	pipe2 "github.com/pipe2-ai/sdk-go"
)

// uploadLocalForRecipe is the cookbook.Client.UploadAsset implementation.
// Wraps the same single-PUT flow as `pipe2 assets upload` but returns
// just the URL — recipes don't need the rest of the CreateAsset row.
//
// Multipart is intentionally skipped. Recipes that need huge sources
// should pass URLs (the standard cookbook input idiom: "upload first,
// then run"). The auto-upload here is a convenience for sub-25MiB
// inputs typical of the kind of clip a recipe author hands in.
func uploadLocalForRecipe(ctx context.Context, client graphql.Client, path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", path, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory; recipes accept files only", path)
	}
	if info.Size() > singlePutThreshold {
		return "", fmt.Errorf("%s is %d bytes; > %d cap for auto-upload — run `pipe2 assets upload` first and pass the URL",
			path, info.Size(), singlePutThreshold)
	}

	contentType := guessContentType(path)
	presign, err := pipe2.RequestUpload(ctx, client, filepath.Base(path), contentType)
	if err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := putToPresignedURL(ctx, presign.Request_upload.Upload_url, contentType, f, info.Size()); err != nil {
		return "", err
	}
	created, err := pipe2.CreateAsset(ctx, client, presign.Request_upload.Key, nil)
	if err != nil {
		return "", err
	}
	return created.Create_asset.Url, nil
}

// guessContentType is a tiny extension-based sniffer; falls back to
// the http stdlib for unknown extensions. Recipes work in a known set
// of media types so this is sufficient.
func guessContentType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	case ".m4a":
		return "audio/mp4"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".srt":
		return "application/x-subrip"
	case ".txt":
		return "text/plain"
	}
	if t := http.DetectContentType(make([]byte, 0)); t != "" {
		return t
	}
	return "application/octet-stream"
}
