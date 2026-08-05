package msbimport

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIsVideoMediaFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"sticker.mp4", true},
		{"sticker.MOV", true},
		{"sticker.webm", true},
		{"sticker.webp", false},
		{"sticker.png", false},
	}

	for _, tt := range tests {
		if got := isVideoMediaFile(tt.path); got != tt.want {
			t.Errorf("isVideoMediaFile(%q) = %t, want %t", tt.path, got, tt.want)
		}
	}
}

func TestMP4ToWebmVideoSticker(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping ffmpeg integration test in short mode")
	}
	if _, err := exec.LookPath(FFMPEG_BIN); err != nil {
		t.Skipf("%s not available: %v", FFMPEG_BIN, err)
	}

	dir := t.TempDir()
	source := filepath.Join(dir, "source.mp4")
	// mpeg4 rather than libx264: the runtime ffmpeg is an LGPL build with no
	// libx264. Only H.264 *encoding* is missing, which nothing here needs -- the
	// h264 decoder that real user uploads rely on is always built in.
	cmd := exec.Command(FFMPEG_BIN,
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc2=size=64x64:rate=10",
		"-t", "1", "-an", "-c:v", "mpeg4", "-pix_fmt", "yuv420p", "-y", source,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create MP4 fixture: %v\n%s", err, out)
	}

	output, err := ConverMediaToTGStickerSmart(source, false)
	if err != nil {
		t.Fatalf("convert MP4 to video sticker: %v", err)
	}
	if filepath.Ext(output) != ".webm" {
		t.Fatalf("output extension = %q, want .webm", filepath.Ext(output))
	}
	if st, err := os.Stat(output); err != nil || st.Size() == 0 {
		t.Fatalf("video sticker output missing or empty: %v", err)
	}
}
