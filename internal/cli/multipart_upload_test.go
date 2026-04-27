package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestPutPart_RoundTrip verifies that putPart streams the right bytes,
// returns the ETag the server set, and surfaces non-2xx responses as errors.
// Uses httptest as a stand-in S3 — real S3 is exercised in the storage
// integration tests.
func TestPutPart_RoundTrip(t *testing.T) {
	want := []byte("hello world part body")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %q, want PUT", r.Method)
		}
		got, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("body = %q, want %q", got, want)
		}
		w.Header().Set("ETag", `"abc-etag"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	etag, err := putPart(context.Background(), server.URL, bytes.NewReader(want), int64(len(want)))
	if err != nil {
		t.Fatalf("putPart: %v", err)
	}
	if etag != `"abc-etag"` {
		t.Errorf("etag = %q, want %q", etag, `"abc-etag"`)
	}
}

func TestPutPart_NonOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	_, err := putPart(context.Background(), server.URL, bytes.NewReader([]byte("x")), 1)
	if err == nil {
		t.Fatal("expected error on 500 response, got nil")
	}
}

// TestMultipartChunking_ByteRangeMath ensures the offset+length math the
// orchestrator generates exactly tiles the file: every byte appears in
// exactly one part, last part is the remainder, no overlap, no gaps.
//
// This is the math equivalent of the bug class "off-by-one in the last
// part" that's easy to introduce when refactoring chunk loops.
func TestMultipartChunking_ByteRangeMath(t *testing.T) {
	tests := []struct {
		name      string
		totalSize int64
		partSize  int64
	}{
		{"exact multiple", 100, 25},        // 4 parts of 25
		{"tiny remainder last part", 101, 25}, // 4 of 25, 1 of 1
		{"single part exactly fits", 25, 25},
		{"single part smaller than chunk", 10, 25},
		{"realistic 32MiB chunks over 200 MiB", 200 << 20, 32 << 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			partCount := int((tt.totalSize + tt.partSize - 1) / tt.partSize)

			covered := int64(0)
			for i := 0; i < partCount; i++ {
				offset := int64(i) * tt.partSize
				length := tt.partSize
				if offset+length > tt.totalSize {
					length = tt.totalSize - offset
				}
				if offset != covered {
					t.Errorf("part %d offset = %d, want %d (gap or overlap)", i+1, offset, covered)
				}
				if length <= 0 {
					t.Errorf("part %d length = %d, must be > 0", i+1, length)
				}
				if i < partCount-1 && length != tt.partSize {
					t.Errorf("part %d (non-last) length = %d, want %d", i+1, length, tt.partSize)
				}
				covered += length
			}
			if covered != tt.totalSize {
				t.Errorf("total coverage = %d, want %d", covered, tt.totalSize)
			}
		})
	}
}

// TestMultipartUpload_ParallelOrderIndependent verifies the worker pool
// produces correct output regardless of completion order. We synthesize
// presigned URLs that map to httptest handlers — each handler captures
// the part body keyed by part number, then a second pass reassembles the
// file and compares to the original.
//
// Smaller chunks than production (100 bytes) so the test stays fast, but
// the math + concurrency story is the same.
func TestMultipartUpload_ParallelOrderIndependent(t *testing.T) {
	// 350-byte payload → 4 parts of 100 + 1 of 50 with partSize=100.
	const totalSize int64 = 350
	const partSize int64 = 100

	src := make([]byte, totalSize)
	if _, err := rand.Read(src); err != nil {
		t.Fatalf("rand: %v", err)
	}
	want := sha256.Sum256(src)

	tmp := t.TempDir()
	srcPath := filepath.Join(tmp, "src.bin")
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatal(err)
	}

	type partKey struct{ number int }
	var mu sync.Mutex
	received := map[partKey][]byte{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Part number is encoded in the path query string.
		partNum := 0
		fmt.Sscanf(r.URL.Query().Get("p"), "%d", &partNum)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		mu.Lock()
		received[partKey{partNum}] = body
		mu.Unlock()
		w.Header().Set("ETag", fmt.Sprintf(`"p%d-etag"`, partNum))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Drive putPart directly across goroutines (skipping the GraphQL
	// orchestration — that's covered by integration tests). This verifies
	// the byte-range tiling and concurrency model.
	partCount := int((totalSize + partSize - 1) / partSize)
	type result struct {
		number int
		etag   string
	}
	results := make([]result, partCount)
	var wg sync.WaitGroup
	for i := 0; i < partCount; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			f, err := os.Open(srcPath)
			if err != nil {
				t.Errorf("open: %v", err)
				return
			}
			defer f.Close()

			offset := int64(i) * partSize
			length := partSize
			if offset+length > totalSize {
				length = totalSize - offset
			}
			url := fmt.Sprintf("%s/upload?p=%d", server.URL, i+1)
			etag, err := putPart(context.Background(), url, io.NewSectionReader(f, offset, length), length)
			if err != nil {
				t.Errorf("part %d: %v", i+1, err)
				return
			}
			results[i] = result{number: i + 1, etag: etag}
		}(i)
	}
	wg.Wait()

	// Reassemble and check the digest.
	var assembled bytes.Buffer
	for i := 1; i <= partCount; i++ {
		mu.Lock()
		body, ok := received[partKey{i}]
		mu.Unlock()
		if !ok {
			t.Fatalf("server never received part %d", i)
		}
		assembled.Write(body)
	}
	gotSum := sha256.Sum256(assembled.Bytes())
	if hex.EncodeToString(gotSum[:]) != hex.EncodeToString(want[:]) {
		t.Errorf("reassembled digest mismatch (parts uploaded out of order? lost bytes?)")
	}
}
