package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Khan/genqlient/graphql"
	pipe2 "github.com/pipe2-ai/sdk-go"
)

// uploadLocalForRecipe is the cookbook.Client.UploadAsset implementation.
// It uploads a local file as an asset and returns just the URL — recipes
// don't need the rest of the CreateAsset row.
//
// Files at or below singlePutThreshold use a single PUT; larger files use
// the same S3 multipart path as `pipe2 assets upload`. The recipe runtime
// now downloads remote sources client-side before calling this (see
// cookbook.ResolveAssetURL), so a fetched full-length video routinely
// exceeds the single-PUT size — multipart is required, not optional.
func uploadLocalForRecipe(ctx context.Context, client graphql.Client, path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", path, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory; recipes accept files only", path)
	}
	if info.Size() == 0 {
		return "", fmt.Errorf("%s is empty", path)
	}

	// Detect + validate the media type and enforce the per-category size
	// cap (10 MiB image / 5 GiB video / 1 GiB audio), same as the
	// stand-alone uploader. A non-media file is rejected here with a clear
	// message rather than after a wasted upload.
	contentType, err := detectContentType(path, "")
	if err != nil {
		return "", err
	}
	category, maxSize, err := mediaCategory(contentType)
	if err != nil {
		return "", err
	}
	if info.Size() > maxSize {
		return "", fmt.Errorf("%s is %d bytes — exceeds %s limit of %d bytes", path, info.Size(), category, maxSize)
	}

	if info.Size() > singlePutThreshold {
		asset, err := multipartUploadAsset(ctx, client, path, contentType, info.Size(), nil, defaultMultipartPartSize, defaultMultipartParallel)
		if err != nil {
			return "", err
		}
		return asset.Url, nil
	}

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
