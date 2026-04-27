package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Khan/genqlient/graphql"

	pipe2 "github.com/pipe2-ai/sdk-go"
)

// partResult is what each worker produces — pairs the part number it
// uploaded with the ETag S3 returned. Workers also record the byte count
// they pushed so the orchestrator can render progress.
type partResult struct {
	PartNumber int
	ETag       string
	Bytes      int64
	Err        error
}

// runMultipartUpload drives the chunked upload flow: request → fan-out →
// complete (or abort on failure / signal). On Ctrl-C the in-progress
// upload is aborted cleanly so we don't leak orphan part objects.
func runMultipartUpload(
	ctx context.Context,
	client graphql.Client,
	path, contentType string,
	totalSize int64,
	tags []string,
	partSize int64,
	parallel int,
) error {
	if partSize <= 0 {
		partSize = defaultMultipartPartSize
	}
	if parallel <= 0 {
		parallel = defaultMultipartParallel
	}

	partCount := int((totalSize + partSize - 1) / partSize)

	Status("starting multipart upload for %s (%d bytes, %d parts of %d MiB, %d workers)...",
		filepath.Base(path), totalSize, partCount, partSize/(1<<20), parallel)

	// Cancel propagation: SIGINT/SIGTERM → cancel(ctx) → workers return →
	// orchestrator runs the abort path. Keep cancel reachable through the
	// whole function so deferred cleanup can call it once on exit.
	uploadCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-uploadCtx.Done():
			return
		case <-sigCh:
			Status("cancel signal received — aborting upload...")
			cancel()
		}
	}()

	// 1. Request the upload + per-part presigned URLs.
	partSizeReq := partSize
	presign, err := pipe2.RequestMultipartUpload(uploadCtx, client, filepath.Base(path), contentType, totalSize, &partSizeReq)
	if err != nil {
		return classifyAPIError(err)
	}
	res := presign.Request_multipart_upload
	if int(res.Part_size) != int(partSize) {
		// Server normalized part_size — adopt it so chunk math is correct.
		partSize = res.Part_size
		partCount = int((totalSize + partSize - 1) / partSize)
	}
	if len(res.Part_urls) != partCount {
		return &ExitError{Code: ExitGeneric, Err: fmt.Errorf(
			"server returned %d part URLs but %d parts expected", len(res.Part_urls), partCount,
		)}
	}

	// At this point we have an open multipart upload on S3. Any error
	// path from here MUST abort it, otherwise the parts pile up until the
	// bucket lifecycle policy reaps them.
	completed := make([]partResult, 0, partCount)
	abortOnFail := func() {
		// Best effort — failing the abort itself is logged but doesn't
		// override the original error we're returning.
		if abortErr := abortUpload(context.Background(), client, res.Upload_id, res.Key); abortErr != nil {
			Status("warning: abort_multipart_upload failed: %v", abortErr)
		}
	}

	// 2. Fan-out part PUTs. Bounded worker pool: at most `parallel` PUTs
	//    in flight at any moment, regardless of how many total parts.
	type job struct {
		index   int    // 0-based, used to index into res.Part_urls
		number  int32  // 1-based, S3 convention
		offset  int64
		length  int64
		url     string
	}

	jobs := make(chan job)
	results := make(chan partResult, partCount)
	var bytesUploaded atomic.Int64

	var wg sync.WaitGroup
	for w := 0; w < parallel; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			// Each worker has its own file handle so reads don't contend
			// for the seek pointer. fileSection() returns a SectionReader
			// view of the byte range, so io.Copy under the hood reads
			// only the assigned slice.
			f, err := os.Open(path)
			if err != nil {
				results <- partResult{Err: fmt.Errorf("worker %d open file: %w", workerID, err)}
				return
			}
			defer f.Close()

			for j := range jobs {
				if uploadCtx.Err() != nil {
					results <- partResult{PartNumber: int(j.number), Err: uploadCtx.Err()}
					continue
				}
				etag, err := putPart(uploadCtx, j.url, io.NewSectionReader(f, j.offset, j.length), j.length)
				if err != nil {
					results <- partResult{PartNumber: int(j.number), Err: err}
					continue
				}
				bytesUploaded.Add(j.length)
				results <- partResult{PartNumber: int(j.number), ETag: etag, Bytes: j.length}
			}
		}(w)
	}

	// Producer: feed every part. Closing `jobs` after the loop lets the
	// workers' for-range exit, after which wg.Wait() completes.
	go func() {
		for i := 0; i < partCount; i++ {
			offset := int64(i) * partSize
			length := partSize
			if offset+length > totalSize {
				length = totalSize - offset
			}
			// res.Part_urls is not guaranteed to be in PartNumber order
			// (genqlient preserves server order, but we don't rely on it).
			url := findPartURL(res.Part_urls, int32(i+1))
			if url == "" {
				results <- partResult{PartNumber: i + 1, Err: fmt.Errorf("no presigned URL for part %d", i+1)}
				continue
			}
			select {
			case <-uploadCtx.Done():
				return
			case jobs <- job{
				index:  i,
				number: int32(i + 1),
				offset: offset,
				length: length,
				url:    url,
			}:
			}
		}
		close(jobs)
	}()

	// Periodic progress on stderr. Suppressed when --json without
	// --verbose (Status() handles that for us).
	progressDone := make(chan struct{})
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-progressDone:
				return
			case <-t.C:
				done := bytesUploaded.Load()
				Status("  ... uploaded %.1f / %.1f MiB", float64(done)/(1<<20), float64(totalSize)/(1<<20))
			}
		}
	}()

	// 3. Drain results. We expect exactly partCount messages, one per
	//    job. First failure triggers cancel + abort and we return.
	var firstErr error
	for i := 0; i < partCount; i++ {
		r := <-results
		if r.Err != nil {
			if firstErr == nil {
				firstErr = r.Err
				cancel() // tells remaining workers to bail
			}
			continue
		}
		completed = append(completed, r)
	}

	close(progressDone)
	wg.Wait()
	close(results)

	if firstErr != nil {
		abortOnFail()
		// If the failure was from cancellation, surface that as the
		// dominant error — easier for the user to recognize "I hit Ctrl-C".
		if errors.Is(firstErr, context.Canceled) {
			return &ExitError{Code: ExitGeneric, Err: errors.New("upload canceled")}
		}
		return &ExitError{Code: ExitGeneric, Err: firstErr}
	}

	// 4. Complete. Sort parts by number — S3's CompleteMultipartUpload
	//    rejects out-of-order lists.
	sort.Slice(completed, func(i, j int) bool { return completed[i].PartNumber < completed[j].PartNumber })

	parts := make([]pipe2.Multipart_part_input, len(completed))
	for i, p := range completed {
		parts[i] = pipe2.Multipart_part_input{
			PartNumber: p.PartNumber,
			ETag:       p.ETag,
		}
	}

	Status("completing upload (registering asset)...")
	resp, err := pipe2.CompleteMultipartUpload(uploadCtx, client, res.Upload_id, res.Key, contentType, parts, tags)
	if err != nil {
		abortOnFail()
		return classifyAPIError(err)
	}
	return Out(resp.Complete_multipart_upload)
}

// putPart streams one part's bytes to its presigned URL. Returns the
// ETag header — required input to CompleteMultipartUpload.
func putPart(ctx context.Context, presignedURL string, body io.Reader, size int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, presignedURL, body)
	if err != nil {
		return "", fmt.Errorf("build PUT request: %w", err)
	}
	req.ContentLength = size

	httpClient := &http.Client{Timeout: partUploadTimeout}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("PUT part: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		buf, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("S3 returned %d on part PUT: %s", resp.StatusCode, string(buf))
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		return "", fmt.Errorf("S3 returned no ETag header on part PUT")
	}
	return etag, nil
}

// abortUpload calls the backend abort action. Used as defer cleanup on
// any error path between request_multipart_upload success and complete.
func abortUpload(ctx context.Context, client graphql.Client, uploadID, key string) error {
	_, err := pipe2.AbortMultipartUpload(ctx, client, uploadID, key)
	return err
}

// findPartURL is a tiny lookup over the server-returned URL list. The
// server typically returns parts 1..N in order, but we don't rely on it.
func findPartURL(parts []pipe2.RequestMultipartUploadRequest_multipart_uploadRequest_multipart_upload_outputPart_urlsMultipart_part_url, number int32) string {
	for _, p := range parts {
		if int32(p.Part_number) == number {
			return p.Url
		}
	}
	return ""
}
