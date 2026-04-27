package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectContentType(t *testing.T) {
	dir := t.TempDir()

	mp4 := filepath.Join(dir, "interview.mp4")
	if err := os.WriteFile(mp4, []byte("fake mp4 bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	noExt := filepath.Join(dir, "rawimage")
	// Minimal JPEG magic so http.DetectContentType identifies it.
	if err := os.WriteFile(noExt, []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0, 0, 0}, 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		path     string
		override string
		want     string // prefix is enough — http.DetectContentType may add params
	}{
		{"override wins", mp4, "audio/wav", "audio/wav"},
		{"extension wins when no override", mp4, "", "video/mp4"},
		{"sniff fallback when no extension", noExt, "", "image/jpeg"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := detectContentType(tt.path, tt.override)
			if err != nil {
				t.Fatalf("detectContentType: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMediaCategory(t *testing.T) {
	tests := []struct {
		ct       string
		wantCat  string
		wantSize int64
		wantErr  bool
	}{
		{"video/mp4", "video", maxVideoUploadSize, false},
		{"image/png", "image", maxImageUploadSize, false},
		{"image/jpeg", "image", maxImageUploadSize, false},
		{"audio/wav", "audio", maxAudioUploadSize, false},
		{"audio/mpeg", "audio", maxAudioUploadSize, false},
		{"text/plain", "", 0, true},
		{"application/pdf", "", 0, true},
		{"", "", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.ct, func(t *testing.T) {
			cat, size, err := mediaCategory(tt.ct)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if cat != tt.wantCat {
				t.Errorf("category = %q, want %q", cat, tt.wantCat)
			}
			if size != tt.wantSize {
				t.Errorf("size = %d, want %d", size, tt.wantSize)
			}
		})
	}
}
