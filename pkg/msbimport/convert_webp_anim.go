package msbimport

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
)

type webmRateControl struct {
	minrate string
	bitrate string
	maxrate string
}

// Output frame rate for animated WebP sources. The sources are variable-rate;
// ffmpeg's fps filter resamples them to this constant rate.
const kakaoWebmOutputFPS = 30.0

var kakaoWebmRateControls = []webmRateControl{
	{bitrate: "610k", maxrate: "910k"},
	{bitrate: "590k", maxrate: "880k"},
	{bitrate: "560k", maxrate: "840k"},
	{bitrate: "530k", maxrate: "800k"},
	{bitrate: "500k", maxrate: "750k"},
	{bitrate: "470k", maxrate: "700k"},
	{bitrate: "440k", maxrate: "660k"},
	{bitrate: "400k", maxrate: "600k"},
	{bitrate: "350k", maxrate: "520k"},
	{bitrate: "300k", maxrate: "450k"},
	{bitrate: "260k", maxrate: "390k"},
	{bitrate: "220k", maxrate: "330k"},
	{bitrate: "180k", maxrate: "270k"},
	{bitrate: "140k", maxrate: "210k"},
	{bitrate: "110k", maxrate: "165k"},
	{bitrate: "90k", maxrate: "135k"},
}

// webmTargetBytes is the size we aim for when picking a starting bitrate. It
// sits a little under Telegram's 255KiB hard limit to leave headroom for the
// encoder overshooting its -b:v target.
const webmTargetBytes = 250 * KiB

// webmBitrateOvershoot is the ratio we assume between the VP9 -b:v target and
// the actual average bitrate of the produced file when picking a starting
// bitrate. maxrate is set to ~1.5x the target, so 1.5 is the worst case: a clip
// that sustains its peak the whole way. Estimating against that worst case makes
// the first encode fit on the first try nearly always, trading a little quality
// on demanding clips for far fewer re-encodes (each one is expensive and, on the
// production VM, prone to timing out).
const webmBitrateOvershoot = 1.50

var webmDurationFallbacks = []string{
	telegramVideoMaxDurationArg,
	telegramVideoSafeDurationArg,
	"00:00:02.400",
	"00:00:02.000",
	"00:00:01.600",
	"00:00:01.200",
}

// KakaoAnimatedWebpToWebm converts Kakao animated WebP stickers to Telegram
// WebM.
func KakaoAnimatedWebpToWebm(f string, status *ConversionStatus) (string, error) {
	return KakaoAnimatedWebpToWebmContext(context.Background(), f, status)
}

func KakaoAnimatedWebpToWebmContext(ctx context.Context, f string, status *ConversionStatus) (string, error) {
	return webpToWebmContext(ctx, f, false, status, telegramVideoMaxDurationArg, false)
}

func animatedWebpToWebmTGVideoContext(ctx context.Context, f string, isCustomEmoji bool, status *ConversionStatus) (string, error) {
	return animatedWebpToWebmTGVideoWithMaxDurationContext(ctx, f, isCustomEmoji, status, telegramVideoMaxDurationArg)
}

// Safe mode handles sources that already exceed Telegram's duration limit, so
// they always get trimmed. Two-pass spends the bit budget across the whole clip
// instead of starving the tail.
func animatedWebpToWebmTGVideoSafeContext(ctx context.Context, f string, isCustomEmoji bool, status *ConversionStatus) (string, error) {
	return webpToWebmContext(ctx, f, isCustomEmoji, status, telegramVideoSafeDurationArg, true)
}

func animatedWebpToWebmTGVideoWithMaxDurationContext(ctx context.Context, f string, isCustomEmoji bool, status *ConversionStatus, maxDuration string) (string, error) {
	return webpToWebmContext(ctx, f, isCustomEmoji, status, maxDuration, false)
}

// webpToWebmContext encodes an animated WebP to Telegram-compliant WebM.
//
// ffmpeg reads the WebP directly: its webp_anim demuxer reports per-frame ANMF
// durations, so the fps filter handles the variable frame timing that this code
// used to reproduce by hand (extracting frames with ImageMagick and duplicating
// them into a constant-rate sequence). Requires FFmpeg 9.0+; older builds skip
// the ANMF chunks entirely and decode nothing.
func webpToWebmContext(ctx context.Context, f string, isCustomEmoji bool, status *ConversionStatus, maxDuration string, twoPass bool) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	pathOut := f + ".webm"
	if err := ctx.Err(); err != nil {
		return pathOut, err
	}
	status.Clear()

	scale := "512:512:force_original_aspect_ratio=decrease"
	if isCustomEmoji {
		scale = "100:100:force_original_aspect_ratio=decrease"
	}

	sourceDurationSec, _ := mediaDurationSeconds(f)
	log.Debugf("webpToWebm: %s dur=%.2fs twoPass=%v", f, sourceDurationSec, twoPass)

	var lastErr error
	for _, duration := range webmDurationAttempts(maxDuration) {
		effDur := effectiveEncodeDuration(sourceDurationSec, duration)
		for i := estimatedWebmRateControlStartIndex(kakaoWebmRateControls, effDur); i < len(kakaoWebmRateControls); {
			rc := kakaoWebmRateControls[i]
			if err := ctx.Err(); err != nil {
				return pathOut, err
			}

			out, err := encodeWebmFromWebp(ctx, f, pathOut, scale, rc, duration, twoPass, status)
			if err != nil {
				if ctx.Err() != nil {
					return pathOut, ctx.Err()
				}
				if errors.Is(err, context.DeadlineExceeded) {
					lastErr = fmt.Errorf("conversion timed out at %s for %s", rc.bitrate, duration)
					log.Warnf("webpToWebm: %v, retrying with shorter duration", lastErr)
					os.Remove(pathOut)
					break
				}
				log.Warnln("webpToWebm ffmpeg ERROR:", out)
				return pathOut, err
			}

			st, err := os.Stat(pathOut)
			if err != nil || st.Size() == 0 {
				lastErr = errors.New("webpToWebm: output empty")
				os.Remove(pathOut)
				i++
				continue
			}
			if st.Size() <= 255*KiB {
				status.Clear()
				return pathOut, nil
			}

			lastErr = fmt.Errorf("webpToWebm: output too large: %d bytes", st.Size())
			status.Set(stickerTooLargeStatus())
			nextIndex := nextWebmRateControlIndexAfterOversize(kakaoWebmRateControls, i, st.Size())
			nextBitrate := "shorter duration"
			if nextIndex < len(kakaoWebmRateControls) {
				nextBitrate = kakaoWebmRateControls[nextIndex].bitrate
			}
			log.Warnf("webpToWebm: output too large at %s for %s, retrying at %s: %d bytes", rc.bitrate, duration, nextBitrate, st.Size())
			os.Remove(pathOut)
			i = nextIndex
		}
	}
	if lastErr != nil {
		return pathOut, fmt.Errorf("%w: %v", ErrStickerTooLarge, lastErr)
	}
	return pathOut, errors.New("webpToWebm: no encode attempts")
}

// encodeWebmFromWebp runs one VP9 encode attempt. Returns ffmpeg's output on
// failure so the caller can log it.
func encodeWebmFromWebp(ctx context.Context, f string, pathOut string, scale string, rc webmRateControl, maxDuration string, twoPass bool, status *ConversionStatus) (string, error) {
	baseArgs := webmFromWebpBaseArgs(f, scale, rc, maxDuration, twoPass)

	// Acquire the slot before starting the timeout so queue wait doesn't eat
	// into the encode budget.
	releaseFFmpeg, slotErr := acquireFFmpegSlot(ctx, status)
	if slotErr != nil {
		return "", slotErr
	}
	defer releaseFFmpeg()

	if !twoPass {
		args := append(append([]string{}, baseArgs...), "-y", pathOut)
		return runWebmEncodePass(ctx, args)
	}

	passDir, err := os.MkdirTemp(filepath.Dir(f), filepath.Base(f)+".passlog-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(passDir)
	passlog := filepath.Join(passDir, "vp9")

	pass1 := append(append([]string{}, baseArgs...), "-pass", "1", "-passlogfile", passlog, "-f", "webm", "-y", os.DevNull)
	if out, err := runWebmEncodePass(ctx, pass1); err != nil {
		return out, err
	}
	pass2 := append(append([]string{}, baseArgs...), "-pass", "2", "-passlogfile", passlog, "-y", pathOut)
	return runWebmEncodePass(ctx, pass2)
}

// webmFromWebpBaseArgs builds the ffmpeg arguments shared by every encode pass,
// minus the output selection.
func webmFromWebpBaseArgs(f string, scale string, rc webmRateControl, maxDuration string, twoPass bool) []string {
	// -cpu-used: VP9 speed/quality tradeoff (0=slowest/best, 8=fastest). Two-pass
	// already costs a second encode, so it buys quality back with a slower preset.
	cpuUsed := "5"
	if twoPass {
		cpuUsed = "4"
	}

	args := append([]string{}, ffmpegQ...)
	args = append(args,
		"-i", f,
		// scale before fps so only the source frames get resampled, not the
		// duplicates the fps filter adds.
		"-vf", fmt.Sprintf("scale=%s,fps=%g", scale, kakaoWebmOutputFPS),
		"-threads", "1", "-pix_fmt", "yuva420p", "-c:v", "libvpx-vp9",
		"-cpu-used", cpuUsed, "-lag-in-frames", "0", "-tile-columns", "0", "-tile-rows", "0", "-auto-alt-ref", "0",
	)
	if rc.minrate != "" {
		args = append(args, "-minrate", rc.minrate)
	}
	args = append(args, "-b:v", rc.bitrate)
	if rc.maxrate != "" {
		args = append(args, "-maxrate", rc.maxrate)
	}
	if twoPass {
		args = append(args, "-g", strconv.Itoa(int(kakaoWebmOutputFPS)))
	}
	return append(args, "-to", maxDuration, "-an")
}

func runWebmEncodePass(ctx context.Context, args []string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, convertCommandTimeout())
	defer cancel()

	out, err := niceLimitedCombinedOutput(runCtx, FFMPEG_BIN, args...)
	if err != nil && runCtx.Err() != nil {
		return string(out), runCtx.Err()
	}
	return string(out), err
}

func stickerTooLargeStatus() string {
	return "too large for Telegram. Compressing..."
}

// estimatedWebmRateControlStartIndex returns the index of the highest-quality
// (highest-bitrate) rate control whose expected output still fits under
// Telegram's size limit for an encode of the given duration. Starting the
// bitrate ladder here avoids an almost-certainly-oversized first encode (and
// its retry) for typical multi-second Kakao stickers, while still starting at
// full quality for short clips that can afford it. Returns 0 when the duration
// is unknown so callers fall back to the previous "start at the top" behaviour.
func estimatedWebmRateControlStartIndex(rateControls []webmRateControl, durationSec float64) int {
	if durationSec <= 0 {
		return 0
	}
	maxKbps := float64(webmTargetBytes) * 8 / durationSec / 1000 / webmBitrateOvershoot
	for i, rc := range rateControls {
		kbps, ok := parseKBitrate(rc.bitrate)
		if !ok {
			continue
		}
		if float64(kbps) <= maxKbps {
			return i
		}
	}
	if len(rateControls) == 0 {
		return 0
	}
	return len(rateControls) - 1
}

// maxDurationArgSeconds parses an ffmpeg "-to" argument such as "00:00:02.400"
// into seconds. Returns 0 when the value can't be parsed.
func maxDurationArgSeconds(arg string) float64 {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return 0
	}
	parts := strings.Split(arg, ":")
	total := 0.0
	for _, p := range parts {
		v, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return 0
		}
		total = total*60 + v
	}
	if total < 0 {
		return 0
	}
	return total
}

// effectiveEncodeDuration is the actual length of the encoded clip: the source
// duration, capped by the current "-to" attempt. Used to size the starting
// bitrate for that attempt.
func effectiveEncodeDuration(sourceDurationSec float64, maxDurationArg string) float64 {
	capSec := maxDurationArgSeconds(maxDurationArg)
	if sourceDurationSec <= 0 {
		return capSec
	}
	if capSec > 0 && capSec < sourceDurationSec {
		return capSec
	}
	return sourceDurationSec
}

func nextWebmRateControlIndexAfterOversize(rateControls []webmRateControl, currentIndex int, outputSize int64) int {
	nextIndex := currentIndex + 1
	if outputSize <= 0 || nextIndex >= len(rateControls) {
		return nextIndex
	}
	currentKbps, ok := parseKBitrate(rateControls[currentIndex].bitrate)
	if !ok || currentKbps <= 0 {
		return nextIndex
	}

	const targetSize = 255 * KiB
	const safetyMargin = 0.94
	estimatedKbps := int(math.Floor(float64(currentKbps) * float64(targetSize) / float64(outputSize) * safetyMargin))
	for i := nextIndex; i < len(rateControls); i++ {
		candidateKbps, ok := parseKBitrate(rateControls[i].bitrate)
		if !ok {
			continue
		}
		if candidateKbps <= estimatedKbps {
			return i
		}
	}
	return nextIndex
}

func parseKBitrate(bitrate string) (int, bool) {
	if !strings.HasSuffix(bitrate, "k") {
		return 0, false
	}
	kbps, err := strconv.Atoi(strings.TrimSuffix(bitrate, "k"))
	if err != nil {
		return 0, false
	}
	return kbps, true
}

func webmDurationAttempts(maxDuration string) []string {
	attempts := []string{}
	seen := map[string]bool{}
	add := func(duration string) {
		if duration == "" || seen[duration] {
			return
		}
		seen[duration] = true
		attempts = append(attempts, duration)
	}
	started := false
	for _, duration := range webmDurationFallbacks {
		if duration == maxDuration {
			started = true
		}
		if started {
			add(duration)
		}
	}
	if len(attempts) == 0 {
		add(maxDuration)
	}
	return attempts
}
