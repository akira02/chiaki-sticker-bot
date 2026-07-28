package msbimport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
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
// WebM. The default path writes PNG frames to disk, then runs two-pass VP9 so
// fast-motion frames get better bit allocation under Telegram's 255KiB limit.
func KakaoAnimatedWebpToWebm(f string, status *ConversionStatus) (string, error) {
	return KakaoAnimatedWebpToWebmContext(context.Background(), f, status)
}

func KakaoAnimatedWebpToWebmContext(ctx context.Context, f string, status *ConversionStatus) (string, error) {
	if os.Getenv("MSB_KAKAO_FAST_PIPE") == "1" && webpHasConstantFrameDelay(f) {
		return webpToWebmViaPipeFastContext(ctx, f, false, status)
	}
	return webpToWebmViaFramesTwoPassContext(ctx, f, false, status)
}

func webpToWebmViaPipeFast(f string, isCustomEmoji bool, status *ConversionStatus) (string, error) {
	return webpToWebmViaPipeFastContext(context.Background(), f, isCustomEmoji, status)
}

func animatedWebpToWebmTGVideoContext(ctx context.Context, f string, isCustomEmoji bool, status *ConversionStatus) (string, error) {
	return animatedWebpToWebmTGVideoWithMaxDurationContext(ctx, f, isCustomEmoji, status, telegramVideoMaxDurationArg)
}

func animatedWebpToWebmTGVideoSafeContext(ctx context.Context, f string, isCustomEmoji bool, status *ConversionStatus) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return f + ".webm", err
	}
	log.Debugln("animatedWebpToWebmTGVideoSafeContext: using frame-sequence path for precise duration trimming.")
	return webpToWebmViaFramesTwoPassWithMaxDurationContext(ctx, f, isCustomEmoji, status, telegramVideoSafeDurationArg)
}

func animatedWebpToWebmTGVideoWithMaxDurationContext(ctx context.Context, f string, isCustomEmoji bool, status *ConversionStatus, maxDuration string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return f + ".webm", err
	}
	if webpHasConstantFrameDelay(f) {
		return webpToWebmViaPipeFastWithMaxDurationContext(ctx, f, isCustomEmoji, status, maxDuration)
	}
	log.Debugln("animatedWebpToWebmTGVideoContext: variable frame delays detected, using frame-sequence path.")
	return webpToWebmViaFramesTwoPassWithMaxDurationContext(ctx, f, isCustomEmoji, status, maxDuration)
}

func webpToWebmViaPipeFastContext(ctx context.Context, f string, isCustomEmoji bool, status *ConversionStatus) (string, error) {
	return webpToWebmViaPipeFastWithMaxDurationContext(ctx, f, isCustomEmoji, status, telegramVideoMaxDurationArg)
}

func webpToWebmViaPipeFastWithMaxDurationContext(ctx context.Context, f string, isCustomEmoji bool, status *ConversionStatus, maxDuration string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	pathOut := f + ".webm"
	status.Clear()

	fps := 25.0
	sourceDurationSec := 0.0
	if delays, ok := webpDelayTicks(f); ok {
		fps = averageFPSFromDelayTicks(delays)
		for _, d := range delays {
			sourceDurationSec += d
		}
		sourceDurationSec /= 100.0
	}
	log.Debugf("webpToWebmViaPipeFast: %s fps=%.2f dur=%.2fs", f, fps, sourceDurationSec)

	scale := "512:512:force_original_aspect_ratio=decrease"
	if isCustomEmoji {
		scale = "100:100:force_original_aspect_ratio=decrease"
	}

	var lastErr error
	for _, duration := range webmDurationAttempts(maxDuration) {
		effDur := effectiveEncodeDuration(sourceDurationSec, duration)
		for i := estimatedWebmRateControlStartIndex(kakaoWebmRateControls, effDur); i < len(kakaoWebmRateControls); {
			rc := kakaoWebmRateControls[i]
			if err := ctx.Err(); err != nil {
				return pathOut, err
			}
			err := webpToWebmViaPipeOnceWithMaxDurationContext(ctx, f, pathOut, scale, fps, rc, duration, status)
			if err != nil {
				lastErr = err
				log.Warnln("webpToWebmViaPipeFast: retrying with two-pass frame sequence fallback.")
				os.Remove(pathOut)
				if fallback, fallbackErr := webpToWebmViaFramesTwoPassWithMaxDurationContext(ctx, f, isCustomEmoji, status, duration); fallbackErr == nil {
					return fallback, nil
				} else {
					log.Warnln("webpToWebmViaPipeFast fallback ERROR:", fallbackErr)
				}
				return pathOut, err
			}
			st, err := os.Stat(pathOut)
			if err != nil || st.Size() == 0 {
				lastErr = errors.New("webpToWebmViaPipeFast: output empty")
				os.Remove(pathOut)
				i++
				continue
			}
			if st.Size() <= 255*KiB {
				status.Clear()
				return pathOut, nil
			}
			lastErr = fmt.Errorf("webpToWebmViaPipeFast: output too large: %d bytes", st.Size())
			status.Set(stickerTooLargeStatus())
			nextIndex := nextWebmRateControlIndexAfterOversize(kakaoWebmRateControls, i, st.Size())
			nextBitrate := "shorter duration"
			if nextIndex < len(kakaoWebmRateControls) {
				nextBitrate = kakaoWebmRateControls[nextIndex].bitrate
			}
			log.Warnf("webpToWebmViaPipeFast: output too large at %s for %s, retrying at %s: %d bytes", rc.bitrate, duration, nextBitrate, st.Size())
			os.Remove(pathOut)
			i = nextIndex
			continue
		}
	}
	if lastErr != nil {
		return pathOut, fmt.Errorf("%w: %v", ErrStickerTooLarge, lastErr)
	}
	return pathOut, errors.New("webpToWebmViaPipeFast: no encode attempts")
}

func webpToWebmViaPipeOnce(f string, pathOut string, scale string, fps float64, rc webmRateControl) error {
	return webpToWebmViaPipeOnceContext(context.Background(), f, pathOut, scale, fps, rc)
}

func webpToWebmViaPipeOnceContext(ctx context.Context, f string, pathOut string, scale string, fps float64, rc webmRateControl) error {
	return webpToWebmViaPipeOnceWithMaxDurationContext(ctx, f, pathOut, scale, fps, rc, telegramVideoMaxDurationArg, nil)
}

func webpToWebmViaPipeOnceWithMaxDurationContext(ctx context.Context, f string, pathOut string, scale string, fps float64, rc webmRateControl, maxDuration string, status *ConversionStatus) error {
	err := webpToWebmViaPipeOnceWithMaxDurationAttempt(ctx, f, pathOut, scale, fps, rc, maxDuration, false, status)
	if err == nil || !processWasKilled(err) || ctxErr(ctx) != nil || sameStringSlice(imageMagickResourceArgs(), imageMagickOOMResourceArgs()) {
		return err
	}

	log.Warnln("webpToWebmViaPipeOnce: process killed, retrying with lower ImageMagick resource limits")
	os.Remove(pathOut)
	return webpToWebmViaPipeOnceWithMaxDurationAttempt(ctx, f, pathOut, scale, fps, rc, maxDuration, true, status)
}

func webpToWebmViaPipeOnceWithMaxDurationAttempt(ctx context.Context, f string, pathOut string, scale string, fps float64, rc webmRateControl, maxDuration string, lowMemoryImageMagick bool, status *ConversionStatus) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ffArgs := append([]string{}, ffmpegQ...)
	ffArgs = append(ffArgs,
		"-f", "image2pipe", "-vcodec", "png",
		"-framerate", fmt.Sprintf("%g", fps),
		"-i", "pipe:0",
		"-vf", "scale="+scale,
		"-threads", "1", "-pix_fmt", "yuva420p", "-c:v", "libvpx-vp9",
		"-cpu-used", "5", "-lag-in-frames", "0", "-tile-columns", "0", "-tile-rows", "0", "-auto-alt-ref", "0",
	)
	if rc.minrate != "" {
		ffArgs = append(ffArgs, "-minrate", rc.minrate)
	}
	ffArgs = append(ffArgs, "-b:v", rc.bitrate)
	if rc.maxrate != "" {
		ffArgs = append(ffArgs, "-maxrate", rc.maxrate)
	}
	ffArgs = append(ffArgs, "-to", maxDuration, "-an", "-y", pathOut)

	imArgs := imageMagickConvertArgs(lowMemoryImageMagick, "WEBP:"+f, "-coalesce", "png:-")

	// Acquire the slot before starting the timeout so queue wait doesn't eat
	// into the encode budget.
	releaseFFmpeg, slotErr := acquireFFmpegSlot(ctx, status)
	if slotErr != nil {
		return slotErr
	}
	runCtx, cancel := context.WithTimeout(ctx, convertCommandTimeout())
	defer cancel()

	imCmd := exec.CommandContext(runCtx, CONVERT_BIN, imArgs...)
	ffCmd := niceCommandContext(runCtx, FFMPEG_BIN, ffArgs...)

	pr, pw := io.Pipe()
	imCmd.Stdout = pw
	var imOut bytes.Buffer
	imCmd.Stderr = &imOut
	var ffOut bytes.Buffer
	ffCmd.Stdin = pr
	ffCmd.Stderr = &ffOut

	if err := imCmd.Start(); err != nil {
		releaseFFmpeg()
		return fmt.Errorf("webpToWebmViaPipeOnce: imCmd start: %w", err)
	}
	if err := ffCmd.Start(); err != nil {
		releaseFFmpeg()
		imCmd.Process.Kill()
		return fmt.Errorf("webpToWebmViaPipeOnce: ffCmd start: %w", err)
	}
	attachCPULimit(ffCmd.Process.Pid)

	imErr := imCmd.Wait()
	pw.Close()
	ffErr := ffCmd.Wait()
	releaseFFmpeg()

	if err := ctx.Err(); err != nil {
		return err
	}
	if imErr != nil || ffErr != nil {
		log.Warnln("webpToWebmViaPipeOnce ERROR ImageMagick:", imOut.String())
		log.Warnln("webpToWebmViaPipeOnce ERROR ffmpeg:", ffOut.String())
		if runCtx.Err() != nil {
			return runCtx.Err()
		}
		if processWasKilled(ffErr) {
			return ffErr
		}
		if processWasKilled(imErr) {
			return imErr
		}
		if ffErr != nil {
			return ffErr
		}
		return imErr
	}
	return nil
}

// webpToWebmViaFramesTwoPass trades temporary disk writes for lower memory and
// better motion quality: ImageMagick exits before ffmpeg starts, then VP9
// two-pass encoding allocates bits across the whole sticker.
func webpToWebmViaFramesTwoPass(f string, isCustomEmoji bool, status *ConversionStatus) (string, error) {
	return webpToWebmViaFramesTwoPassContext(context.Background(), f, isCustomEmoji, status)
}

func webpToWebmViaFramesTwoPassContext(ctx context.Context, f string, isCustomEmoji bool, status *ConversionStatus) (string, error) {
	return webpToWebmViaFramesTwoPassWithMaxDurationContext(ctx, f, isCustomEmoji, status, telegramVideoMaxDurationArg)
}

func webpToWebmViaFramesTwoPassWithMaxDuration(f string, isCustomEmoji bool, status *ConversionStatus, maxDuration string) (string, error) {
	return webpToWebmViaFramesTwoPassWithMaxDurationContext(context.Background(), f, isCustomEmoji, status, maxDuration)
}

func webpToWebmViaFramesTwoPassWithMaxDurationContext(ctx context.Context, f string, isCustomEmoji bool, status *ConversionStatus, maxDuration string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	pathOut := f + ".webm"
	status.Clear()
	frameDir, err := os.MkdirTemp(filepath.Dir(f), filepath.Base(f)+".frames-*")
	if err != nil {
		return pathOut, err
	}
	defer os.RemoveAll(frameDir)

	framePattern := filepath.Join(frameDir, "frame-%05d.png")
	size := "512x512>"
	scale := "512:512:force_original_aspect_ratio=decrease"
	if isCustomEmoji {
		size = "100x100>"
		scale = "100:100:force_original_aspect_ratio=decrease"
	}

	imArgs := []string{"WEBP:" + f, "-coalesce", "-resize", size, framePattern}
	imOut, err := runImageMagickConvertHeavyWithOOMRetry(ctx, imageMagickTimeout, status, imArgs...)
	if err != nil {
		if ctx.Err() != nil {
			return pathOut, ctx.Err()
		}
		log.Warnln("webpToWebmViaFramesTwoPass ImageMagick ERROR:", string(imOut))
		return pathOut, err
	}
	frames, err := filepath.Glob(filepath.Join(frameDir, "frame-*.png"))
	if err != nil || len(frames) == 0 {
		return pathOut, errors.New("webpToWebmViaFramesTwoPass: no frames produced")
	}
	timing := webpTimingForFrames(f, len(frames))
	timedFramePattern, timedFrameCount, err := materializeTimedFrameSequence(frameDir, frames, timing.durations, timing.outputFPS)
	if err != nil {
		return pathOut, err
	}
	encodeDurationSec := 0.0
	if timing.outputFPS > 0 {
		encodeDurationSec = float64(timedFrameCount) / timing.outputFPS
	}

	var lastErr error
	for _, duration := range webmDurationAttempts(maxDuration) {
		effDur := effectiveEncodeDuration(encodeDurationSec, duration)
		for i := estimatedWebmRateControlStartIndex(kakaoWebmRateControls, effDur); i < len(kakaoWebmRateControls); {
			rc := kakaoWebmRateControls[i]
			if err := ctx.Err(); err != nil {
				return pathOut, err
			}
			out, err := encodeWebmFramesTwoPass(ctx, timedFramePattern, pathOut, scale, timing.outputFPS, frameDir, rc, duration, status)
			if err != nil {
				if ctx.Err() != nil {
					return pathOut, ctx.Err()
				}
				if errors.Is(err, context.DeadlineExceeded) {
					lastErr = fmt.Errorf("conversion timed out at %s for %s", rc.bitrate, duration)
					log.Warnf("webpToWebmViaFramesTwoPass: %v, retrying with shorter duration", lastErr)
					os.Remove(pathOut)
					break
				}
				log.Warnln("webpToWebmViaFramesTwoPass ffmpeg ERROR:", string(out))
				return pathOut, err
			}
			st, err := os.Stat(pathOut)
			if err != nil || st.Size() == 0 {
				lastErr = errors.New("webpToWebmViaFramesTwoPass: output empty")
				os.Remove(pathOut)
				i++
				continue
			}
			if st.Size() <= 255*KiB {
				status.Clear()
				return pathOut, nil
			}
			lastErr = fmt.Errorf("webpToWebmViaFramesTwoPass: output too large: %d bytes", st.Size())
			status.Set(stickerTooLargeStatus())
			nextIndex := nextWebmRateControlIndexAfterOversize(kakaoWebmRateControls, i, st.Size())
			nextBitrate := "shorter duration"
			if nextIndex < len(kakaoWebmRateControls) {
				nextBitrate = kakaoWebmRateControls[nextIndex].bitrate
			}
			log.Warnf("webpToWebmViaFramesTwoPass: output too large at %s for %s, retrying at %s: %d bytes", rc.bitrate, duration, nextBitrate, st.Size())
			os.Remove(pathOut)
			i = nextIndex
			continue
		}
	}
	if lastErr != nil {
		return pathOut, fmt.Errorf("%w: %v", ErrStickerTooLarge, lastErr)
	}
	return pathOut, errors.New("webpToWebmViaFramesTwoPass: no encode attempts")
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

func encodeWebmFramesTwoPass(ctx context.Context, framePattern string, pathOut string, scale string, fps float64, workDir string, rc webmRateControl, maxDuration string, status *ConversionStatus) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	keyframeInterval := int(fps + 0.5)
	if keyframeInterval < 12 {
		keyframeInterval = 12
	}
	if keyframeInterval > 30 {
		keyframeInterval = 30
	}

	passlog := filepath.Join(workDir, "vp9-passlog")
	baseArgs := append([]string{}, ffmpegQ...)
	baseArgs = append(baseArgs,
		"-framerate", fmt.Sprintf("%g", fps),
		"-i", framePattern,
		"-vf", "scale="+scale,
		"-threads", "1", "-pix_fmt", "yuva420p", "-c:v", "libvpx-vp9",
		"-cpu-used", "4", "-lag-in-frames", "0", "-tile-columns", "0", "-tile-rows", "0", "-auto-alt-ref", "0",
		"-b:v", rc.bitrate, "-maxrate", rc.maxrate,
		"-g", strconv.Itoa(keyframeInterval),
		"-to", maxDuration, "-an",
	)

	releaseFFmpeg, slotErr := acquireFFmpegSlot(ctx, status)
	if slotErr != nil {
		return "", slotErr
	}
	defer releaseFFmpeg()

	pass1Args := append([]string{}, baseArgs...)
	pass1Args = append(pass1Args, "-pass", "1", "-passlogfile", passlog, "-f", "webm", "-y", os.DevNull)
	runCtx, cancel := context.WithTimeout(ctx, convertCommandTimeout())
	out, err := niceLimitedCombinedOutput(runCtx, FFMPEG_BIN, pass1Args...)
	runErr := runCtx.Err()
	cancel()
	if err != nil {
		if runErr != nil {
			return string(out), runErr
		}
		return string(out), err
	}

	pass2Args := append([]string{}, baseArgs...)
	pass2Args = append(pass2Args, "-pass", "2", "-passlogfile", passlog, "-y", pathOut)
	runCtx, cancel = context.WithTimeout(ctx, convertCommandTimeout())
	out, err = niceLimitedCombinedOutput(runCtx, FFMPEG_BIN, pass2Args...)
	runErr = runCtx.Err()
	cancel()
	if err != nil {
		if runErr != nil {
			return string(out), runErr
		}
		return string(out), err
	}
	return "", nil
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

type webpTiming struct {
	durations []float64
	outputFPS float64
}

func webpTimingForFrames(f string, frameCount int) webpTiming {
	durations, ok := webpFrameDurations(f, frameCount)
	if !ok {
		fps := webpFPS(f)
		if fps <= 0 {
			fps = 25
		}
		durations = fallbackFrameDurations(frameCount, fps)
	}
	return webpTiming{
		durations: durations,
		outputFPS: kakaoWebmOutputFPS,
	}
}

func materializeTimedFrameSequence(parentDir string, frames []string, durations []float64, fps float64) (string, int, error) {
	if len(frames) == 0 {
		return "", 0, errors.New("materializeTimedFrameSequence: no frames")
	}
	if len(frames) != len(durations) {
		return "", 0, errors.New("materializeTimedFrameSequence: frame/duration count mismatch")
	}
	if fps <= 0 {
		fps = kakaoWebmOutputFPS
	}

	timedDir := filepath.Join(parentDir, "timed")
	if err := os.MkdirAll(timedDir, 0755); err != nil {
		return "", 0, err
	}

	maxFrames := int(math.Ceil(telegramVideoMaxDuration * fps))
	if maxFrames < 1 {
		maxFrames = 1
	}

	outFrameCount := 0
	for i, frame := range frames {
		repeat := int(math.Round(durations[i] * fps))
		if repeat < 1 {
			repeat = 1
		}
		for j := 0; j < repeat && outFrameCount < maxFrames; j++ {
			dst := filepath.Join(timedDir, fmt.Sprintf("frame-%05d.png", outFrameCount))
			if err := linkOrCopyFrame(frame, dst); err != nil {
				return "", 0, err
			}
			outFrameCount++
		}
		if outFrameCount >= maxFrames {
			break
		}
	}
	if outFrameCount == 0 {
		return "", 0, errors.New("materializeTimedFrameSequence: no timed frames produced")
	}

	return filepath.Join(timedDir, "frame-%05d.png"), outFrameCount, nil
}

func linkOrCopyFrame(src string, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return nil
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func webpFrameDurations(f string, frameCount int) ([]float64, bool) {
	delays, ok := webpDelayTicks(f)
	if !ok {
		return nil, false
	}
	return normalizeFrameDurations(delays, frameCount), true
}

func normalizeFrameDurations(delayTicks []float64, frameCount int) []float64 {
	if frameCount <= 0 {
		return nil
	}
	if len(delayTicks) == 0 {
		return fallbackFrameDurations(frameCount, 25)
	}

	durations := make([]float64, frameCount)
	for i := 0; i < frameCount; i++ {
		delayIndex := i
		if delayIndex >= len(delayTicks) {
			delayIndex = len(delayTicks) - 1
		}
		durations[i] = delayTicks[delayIndex] / 100.0
	}
	return durations
}

func fallbackFrameDurations(frameCount int, fps float64) []float64 {
	if fps <= 0 {
		fps = 25
	}
	durations := make([]float64, frameCount)
	duration := 1.0 / fps
	for i := range durations {
		durations[i] = duration
	}
	return durations
}

// webpFPS returns the average playback FPS of an animated WebP from all frame
// delays reported by ImageMagick. Falls back to 25 if unknown.
func webpFPS(f string) float64 {
	delays, ok := webpDelayTicks(f)
	if !ok {
		return 25
	}
	return averageFPSFromDelayTicks(delays)
}

func averageFPSFromDelayTicks(delays []float64) float64 {
	totalTicks := 0.0
	for _, delay := range delays {
		totalTicks += delay
	}
	if totalTicks <= 0 {
		return 25
	}
	return float64(len(delays)) * 100.0 / totalTicks
}

func webpDelayTicks(f string) ([]float64, bool) {
	out, err := commandOutputWithTimeout(IDENTIFY_BIN,
		append(IDENTIFY_ARGS, "-format", "%T\n", "WEBP:"+f)...,
	)
	if err != nil || len(out) == 0 {
		return nil, false
	}
	return parseWebpDelayTicks(string(out))
}

func parseWebpDelayTicks(out string) ([]float64, bool) {
	var delays []float64
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		delay, err := strconv.ParseFloat(strings.TrimSpace(line), 64)
		if err != nil || delay <= 0 {
			return nil, false
		}
		delays = append(delays, delay)
	}
	if len(delays) == 0 {
		return nil, false
	}
	return delays, true
}

func webpHasConstantFrameDelay(f string) bool {
	delays, ok := webpDelayTicks(f)
	if !ok {
		return false
	}
	if len(delays) < 2 {
		return true
	}
	first := delays[0]
	for _, delay := range delays[1:] {
		if math.Abs(delay-first) > 0.001 {
			return false
		}
	}
	return true
}
