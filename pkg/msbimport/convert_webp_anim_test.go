package msbimport

import (
	"math"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The fps filter is what converts the WebP's variable ANMF frame delays into a
// constant-rate stream. Dropping it would silently mistime every animated
// sticker rather than fail, so pin it down.
func TestWebmFromWebpBaseArgsResamplesToConstantRate(t *testing.T) {
	rc := webmRateControl{bitrate: "610k", maxrate: "910k"}
	args := webmFromWebpBaseArgs("in.webp", "512:512:force_original_aspect_ratio=decrease", rc, telegramVideoMaxDurationArg, false)

	joined := strings.Join(args, " ")
	wantFilter := "scale=512:512:force_original_aspect_ratio=decrease,fps=30"
	if !strings.Contains(joined, wantFilter) {
		t.Fatalf("filter chain missing %q: %v", wantFilter, args)
	}
	// The WebP must be read directly; an image2pipe input would mean the
	// ImageMagick frame-extraction detour is back.
	if strings.Contains(joined, "image2pipe") {
		t.Fatalf("expected direct WebP input, got: %v", args)
	}
	for _, want := range []string{"-pix_fmt yuva420p", "-i in.webp", "-to " + telegramVideoMaxDurationArg} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args missing %q: %v", want, args)
		}
	}
	if strings.Contains(joined, "-g ") {
		t.Fatalf("single-pass encode should not set a keyframe interval: %v", args)
	}
}

func TestWebmFromWebpBaseArgsTwoPassSetsKeyframeInterval(t *testing.T) {
	rc := webmRateControl{bitrate: "610k", maxrate: "910k"}
	args := webmFromWebpBaseArgs("in.webp", "100:100", rc, telegramVideoSafeDurationArg, true)

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-g 30") {
		t.Fatalf("two-pass encode should pin the keyframe interval to the output fps: %v", args)
	}
	if !strings.Contains(joined, "-to "+telegramVideoSafeDurationArg) {
		t.Fatalf("args missing safe duration: %v", args)
	}
}

func TestWebmDurationAttemptsOnlyShortens(t *testing.T) {
	tests := []struct {
		name string
		max  string
		want []string
	}{
		{
			name: "max duration",
			max:  telegramVideoMaxDurationArg,
			want: []string{
				telegramVideoMaxDurationArg,
				telegramVideoSafeDurationArg,
				"00:00:02.400",
				"00:00:02.000",
				"00:00:01.600",
				"00:00:01.200",
			},
		},
		{
			name: "safe duration",
			max:  telegramVideoSafeDurationArg,
			want: []string{
				telegramVideoSafeDurationArg,
				"00:00:02.400",
				"00:00:02.000",
				"00:00:01.600",
				"00:00:01.200",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := webmDurationAttempts(tt.max)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d: %v", len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("attempt[%d] = %s, want %s", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestNextWebmRateControlIndexAfterOversizeSkipsClearlyTooHighBitrates(t *testing.T) {
	tests := []struct {
		name         string
		currentIndex int
		outputSize   int64
		wantIndex    int
	}{
		{
			name:         "large oversize skips from 610k to 470k",
			currentIndex: 0,
			outputSize:   314168,
			wantIndex:    5,
		},
		{
			name:         "moderate oversize skips from 610k to 530k",
			currentIndex: 0,
			outputSize:   277744,
			wantIndex:    3,
		},
		{
			name:         "small oversize skips from 560k to 500k",
			currentIndex: 2,
			outputSize:   262688,
			wantIndex:    4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nextWebmRateControlIndexAfterOversize(kakaoWebmRateControls, tt.currentIndex, tt.outputSize)
			if got != tt.wantIndex {
				t.Fatalf("next index = %d (%s), want %d (%s)",
					got, kakaoWebmRateControls[got].bitrate,
					tt.wantIndex, kakaoWebmRateControls[tt.wantIndex].bitrate)
			}
		})
	}
}

func TestEstimatedWebmRateControlStartIndex(t *testing.T) {
	tests := []struct {
		name        string
		durationSec float64
		wantBitrate string
	}{
		// Unknown duration must fall back to the top of the ladder so behaviour
		// is unchanged when we can't measure the source.
		{name: "unknown duration starts at top", durationSec: 0, wantBitrate: "610k"},
		// A full 3s sticker cannot fit at 610k (observed 288-316KiB), so we must
		// start well below it rather than waste the heaviest encode. At the
		// worst-case 1.5x overshoot this lands at 440k.
		{name: "3s starts below 610k", durationSec: 3.0, wantBitrate: "440k"},
		// Short clips can afford full quality on the first try.
		{name: "1s affords top bitrate", durationSec: 1.0, wantBitrate: "610k"},
		{name: "2s affords top bitrate", durationSec: 2.0, wantBitrate: "610k"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := estimatedWebmRateControlStartIndex(kakaoWebmRateControls, tt.durationSec)
			if got := kakaoWebmRateControls[i].bitrate; got != tt.wantBitrate {
				t.Fatalf("start index %d = %s, want %s", i, got, tt.wantBitrate)
			}
		})
	}
}

// TestEstimatedStartIndexFitsObservedOutput guards the core promise: for the
// real 3s stickers that overshot at 610k in production, the estimated starting
// bitrate would have produced a file within Telegram's limit on the first try.
func TestEstimatedStartIndexFitsObservedOutput(t *testing.T) {
	const observed610kBytes = 316802 // largest oversize seen at 610k over 3s
	observedKbps, _ := parseKBitrate("610k")
	i := estimatedWebmRateControlStartIndex(kakaoWebmRateControls, 3.0)
	startKbps, _ := parseKBitrate(kakaoWebmRateControls[i].bitrate)

	// Scale the observed size down by the bitrate ratio (size ∝ bitrate at a
	// fixed duration) and confirm it clears the 255KiB limit.
	predicted := float64(observed610kBytes) * float64(startKbps) / float64(observedKbps)
	if predicted > 255*KiB {
		t.Fatalf("predicted %.0f bytes at %s exceeds 255KiB", predicted, kakaoWebmRateControls[i].bitrate)
	}
}

func TestMaxDurationArgSeconds(t *testing.T) {
	tests := []struct {
		arg  string
		want float64
	}{
		{"00:00:03", 3},
		{"00:00:02.400", 2.4},
		{"00:01:00", 60},
		{"01:00:00", 3600},
		{"", 0},
		{"bogus", 0},
	}
	for _, tt := range tests {
		if got := maxDurationArgSeconds(tt.arg); got != tt.want {
			t.Fatalf("maxDurationArgSeconds(%q) = %v, want %v", tt.arg, got, tt.want)
		}
	}
}

func TestKakaoAnimatedWebpToWebmPreservesVariableDelayDuration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping ffmpeg/ImageMagick integration test in short mode")
	}

	InitConvert()
	for _, bin := range []string{CONVERT_BIN, IDENTIFY_BIN, FFMPEG_BIN, FFPROBE_BIN} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available: %v", bin, err)
		}
	}
	requireAnimatedWebpSupport(t)

	dir := t.TempDir()
	red := filepath.Join(dir, "red.png")
	green := filepath.Join(dir, "green.png")
	blue := filepath.Join(dir, "blue.png")
	writeSolidPNG(t, red, "red")
	writeSolidPNG(t, green, "green")
	writeSolidPNG(t, blue, "blue")

	source := filepath.Join(dir, "source.webp")
	args := append([]string{}, CONVERT_ARGS...)
	args = append(args,
		"-delay", "10", red,
		"-delay", "100", green,
		"-delay", "100", blue,
		"-loop", "0", source,
	)
	if out, err := exec.Command(CONVERT_BIN, args...).CombinedOutput(); err != nil {
		t.Fatalf("create animated webp: %v\n%s", err, string(out))
	}

	// Derive the expectation from what ImageMagick actually wrote rather than
	// from the -delay flags: IM versions disagree about how -delay maps onto
	// frames, so a hardcoded duration makes this test flaky per environment.
	sourceDuration := webpTicksDurationForTest(t, source)
	want := math.Min(sourceDuration, telegramVideoMaxDuration)

	webm, err := KakaoAnimatedWebpToWebm(source, NewConversionStatus())
	if err != nil {
		t.Fatalf("KakaoAnimatedWebpToWebm returned error: %v", err)
	}

	// Tolerance covers the fps filter quantising to 1/30s plus the demuxer
	// deriving the final frame's duration rather than reading it.
	duration := ffprobeDurationForTest(t, webm)
	if math.Abs(duration-want) > 0.15 {
		t.Fatalf("duration = %.3fs, want %.3fs (source %.3fs)", duration, want, sourceDuration)
	}
}

// webpTicksDurationForTest sums an animated WebP's frame delays via
// ImageMagick, giving ground truth that does not depend on the ffmpeg build
// under test.
func webpTicksDurationForTest(t *testing.T, path string) float64 {
	t.Helper()
	args := append([]string{}, IDENTIFY_ARGS...)
	args = append(args, "-format", "%T\n", "WEBP:"+path)
	out, err := exec.Command(IDENTIFY_BIN, args...).Output()
	if err != nil {
		t.Fatalf("identify delays: %v", err)
	}
	ticks := 0.0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		v, err := strconv.ParseFloat(strings.TrimSpace(line), 64)
		if err != nil {
			t.Fatalf("parse delay tick %q: %v", line, err)
		}
		ticks += v
	}
	if ticks <= 0 {
		t.Fatalf("source has no frame delays")
	}
	return ticks / 100.0
}

func TestFFToWebmSafeAnimatedWebpUsesSafeDuration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping ffmpeg/ImageMagick integration test in short mode")
	}

	InitConvert()
	for _, bin := range []string{CONVERT_BIN, IDENTIFY_BIN, FFMPEG_BIN, FFPROBE_BIN} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available: %v", bin, err)
		}
	}
	requireAnimatedWebpSupport(t)

	dir := t.TempDir()
	red := filepath.Join(dir, "red.png")
	green := filepath.Join(dir, "green.png")
	blue := filepath.Join(dir, "blue.png")
	yellow := filepath.Join(dir, "yellow.png")
	writeSolidPNG(t, red, "red")
	writeSolidPNG(t, green, "green")
	writeSolidPNG(t, blue, "blue")
	writeSolidPNG(t, yellow, "yellow")

	source := filepath.Join(dir, "safe-source.webp")
	args := append([]string{}, CONVERT_ARGS...)
	args = append(args,
		"-delay", "100", red,
		"-delay", "100", green,
		"-delay", "100", blue,
		"-delay", "100", yellow,
		"-loop", "0", source,
	)
	if out, err := exec.Command(CONVERT_BIN, args...).CombinedOutput(); err != nil {
		t.Fatalf("create animated webp: %v\n%s", err, string(out))
	}

	webm, err := FFToWebmSafe(source, false)
	if err != nil {
		t.Fatalf("FFToWebmSafe returned error: %v", err)
	}

	duration := ffprobeDurationForTest(t, webm)
	if duration > 2.95 {
		t.Fatalf("duration = %.3fs, want safe duration below 2.95s", duration)
	}
}

// requireAnimatedWebpSupport skips when ffmpeg predates the animated WebP
// demuxer (FFmpeg 9.0). Older builds skip the ANMF chunks and decode nothing,
// so these tests would report a toolchain gap as a conversion bug.
func requireAnimatedWebpSupport(t *testing.T) {
	t.Helper()
	out, err := exec.Command(FFMPEG_BIN, "-hide_banner", "-demuxers").Output()
	if err != nil {
		t.Skipf("cannot list ffmpeg demuxers: %v", err)
	}
	if !strings.Contains(string(out), "webp_anim") {
		t.Skip("ffmpeg lacks the webp_anim demuxer; needs FFmpeg 9.0+")
	}
}

func writeSolidPNG(t *testing.T, path string, color string) {
	t.Helper()
	args := append([]string{}, CONVERT_ARGS...)
	args = append(args, "-size", "64x64", "xc:"+color, path)
	if out, err := exec.Command(CONVERT_BIN, args...).CombinedOutput(); err != nil {
		t.Fatalf("create %s png: %v\n%s", color, err, string(out))
	}
}

func ffprobeDurationForTest(t *testing.T, path string) float64 {
	t.Helper()
	out, err := exec.Command(FFPROBE_BIN,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=nw=1:nk=1",
		path,
	).Output()
	if err != nil {
		t.Fatalf("ffprobe duration: %v", err)
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		t.Fatalf("parse ffprobe duration %q: %v", strings.TrimSpace(string(out)), err)
	}
	return duration
}
