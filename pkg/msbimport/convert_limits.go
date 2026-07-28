package msbimport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"
)

// Hard ceiling for a single ffmpeg invocation. With a pool size of 1 a hung
// ffmpeg would otherwise block every queued conversion indefinitely.
const ffmpegTimeout = 120 * time.Second

// Hard ceiling for ImageMagick operations. Static conversions are usually
// quick; this keeps a stuck convert process from blocking an import forever.
const imageMagickTimeout = 120 * time.Second

// Hard ceiling for CDN archive downloads. The normal HTTP client has a short
// timeout, but curl-based archive downloads need their own guard.
const archiveDownloadTimeout = 120 * time.Second

// Telegram rejects video stickers longer than 3s. Sources beyond this can skip
// the first regular encode and go straight to safe mode, while sources at or
// below the limit still get a normal encode so we avoid trimming unnecessarily.
const telegramVideoMaxDuration = 3.0
const telegramVideoMaxDurationArg = "00:00:03"
const telegramVideoSafeDurationArg = "00:00:02.800"

// CPU-heavy encodes (VP9) run niced so the HTTP/health-check goroutine keeps
// getting CPU on the shared single-core VM. `nice` exec-replaces itself with the
// target binary (same PID), so CommandContext timeouts still reach ffmpeg.
const niceLevel = "19"

// ImageMagick pixel-cache limits. `memory` is the in-RAM ceiling; past it the
// cache spills to a memory-mapped file (`map`), then to disk. Coalescing a Kakao
// animated WebP holds every frame at source resolution (~1MB/frame at 512px), so
// a low memory limit forces slow mmap/disk spill on the hot path. Observed peak
// RSS on the 256MB VM sits around 164MB, leaving headroom to keep more of that
// cache resident. The OOM values are the fallback used only after a kill, so
// they stay small. All overridable via MSB_IM_* env vars.
const (
	defaultImageMagickMemoryLimit    = "104MiB"
	defaultImageMagickMapLimit       = "128MiB"
	defaultImageMagickOOMMemoryLimit = "32MiB"
	defaultImageMagickOOMMapLimit    = "64MiB"
)

func niceCommand(bin string, args ...string) *exec.Cmd {
	return exec.Command("nice", append([]string{"-n", niceLevel, bin}, args...)...)
}

func niceCommandContext(ctx context.Context, bin string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "nice", append([]string{"-n", niceLevel, bin}, args...)...)
}

// cpuLimitPercent is the per-core CPU cap for heavy encoders, expressed as a
// percentage of a single core (100 = one full core). 0 disables capping.
// On fly's shared-cpu VM, `nice` alone can't keep the webhook/health handler
// responsive because the whole VM gets throttled at the platform level once it
// exceeds its baseline; capping the encoder keeps total usage under that ceiling.
func cpuLimitPercent() int {
	if v, err := strconv.Atoi(os.Getenv("MSB_CPU_LIMIT")); err == nil && v > 0 {
		return v
	}
	return 0
}

// attachCPULimit throttles an already-started heavy process to cpuLimitPercent()
// of a single core via cpulimit(1). It attaches by PID and self-exits when the
// target dies (-z), so it never orphans and needs no explicit teardown. No-op
// when disabled or when cpulimit isn't installed.
func attachCPULimit(pid int) {
	limit := cpuLimitPercent()
	if limit <= 0 || pid <= 0 {
		return
	}
	cl := exec.Command("cpulimit", "-p", strconv.Itoa(pid), "-l", strconv.Itoa(limit), "-z")
	if err := cl.Start(); err != nil {
		log.Warnln("attachCPULimit: failed to start cpulimit:", err)
		return
	}
	go cl.Wait() // reap the self-exiting cpulimit process
}

// niceLimitedCombinedOutput runs bin under nice (and cpulimit if configured) and
// returns combined stdout+stderr. ctx cancellation still reaches the target:
// `nice` exec-replaces itself with bin (same PID), so the CommandContext SIGKILL
// hits the encoder directly, and cpulimit self-exits once that PID is gone.
func niceLimitedCombinedOutput(ctx context.Context, bin string, args ...string) ([]byte, error) {
	cmd := niceCommandContext(ctx, bin, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Start(); err != nil {
		return buf.Bytes(), err
	}
	attachCPULimit(cmd.Process.Pid)
	err := cmd.Wait()
	return buf.Bytes(), err
}

func commandOutputWithTimeout(bin string, args ...string) ([]byte, error) {
	timeout := convertCommandTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	if err != nil && ctx.Err() != nil {
		return out, fmt.Errorf("%w: %s timed out after %s", ctx.Err(), bin, timeout)
	}
	return out, err
}

func commandRunWithTimeout(bin string, args ...string) error {
	_, err := commandOutputWithTimeout(bin, args...)
	return err
}

func convertCommandTimeout() time.Duration {
	timeout := ffmpegTimeout
	if value, err := strconv.Atoi(os.Getenv("MSB_CONVERT_TIMEOUT_SECONDS")); err == nil && value > 0 {
		timeout = time.Duration(value) * time.Second
	}
	return timeout
}

func imageMagickResourceArgs() []string {
	memoryLimit := os.Getenv("MSB_IM_MEMORY_LIMIT")
	if memoryLimit == "" {
		memoryLimit = defaultImageMagickMemoryLimit
	}
	mapLimit := os.Getenv("MSB_IM_MAP_LIMIT")
	if mapLimit == "" {
		mapLimit = defaultImageMagickMapLimit
	}
	return imageMagickResourceArgsFromLimits(memoryLimit, mapLimit)
}

func imageMagickOOMResourceArgs() []string {
	memoryLimit := os.Getenv("MSB_IM_OOM_MEMORY_LIMIT")
	if memoryLimit == "" {
		memoryLimit = defaultImageMagickOOMMemoryLimit
	}
	mapLimit := os.Getenv("MSB_IM_OOM_MAP_LIMIT")
	if mapLimit == "" {
		mapLimit = defaultImageMagickOOMMapLimit
	}
	return imageMagickResourceArgsFromLimits(memoryLimit, mapLimit)
}

func imageMagickResourceArgsFromLimits(memoryLimit string, mapLimit string) []string {
	args := []string{}
	if memoryLimit != "0" {
		args = append(args, "-limit", "memory", memoryLimit)
	}
	if mapLimit != "0" {
		args = append(args, "-limit", "map", mapLimit)
	}
	return args
}

func imageMagickConvertArgs(lowMemory bool, args ...string) []string {
	fullArgs := append([]string{}, CONVERT_ARGS...)
	if lowMemory {
		fullArgs = append(fullArgs, imageMagickOOMResourceArgs()...)
	} else {
		fullArgs = append(fullArgs, imageMagickResourceArgs()...)
	}
	fullArgs = append(fullArgs, args...)
	return fullArgs
}

// imRunOpts selects which converter queue an ImageMagick call waits in and where
// its queue position is reported.
type imRunOpts struct {
	// heavy marks coalescing/frame-extracting work, which holds every frame in
	// the pixel cache and must not run alongside ffmpeg.
	heavy  bool
	status *ConversionStatus
}

func runImageMagickConvertWithOOMRetry(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	return runImageMagickConvertOpts(ctx, timeout, imRunOpts{}, args...)
}

func runImageMagickConvertHeavyWithOOMRetry(ctx context.Context, timeout time.Duration, status *ConversionStatus, args ...string) ([]byte, error) {
	return runImageMagickConvertOpts(ctx, timeout, imRunOpts{heavy: true, status: status}, args...)
}

func runImageMagickConvertOpts(ctx context.Context, timeout time.Duration, opts imRunOpts, args ...string) ([]byte, error) {
	out, err := runImageMagickConvert(ctx, timeout, false, opts, args...)
	if err == nil || !processWasKilled(err) || ctxErr(ctx) != nil || sameStringSlice(imageMagickResourceArgs(), imageMagickOOMResourceArgs()) {
		return out, err
	}

	log.Warnf("ImageMagick was killed, retrying with lower resource limits: %s", strings.Join(imageMagickOOMResourceArgs(), " "))
	return runImageMagickConvert(ctx, timeout, true, opts, args...)
}

func runImageMagickConvert(ctx context.Context, timeout time.Duration, lowMemory bool, opts imRunOpts, args ...string) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	// Acquire the slot before starting the timeout so queue wait doesn't eat
	// into the conversion budget.
	releaseSlot, err := acquireImageMagickSlot(ctx, opts.status, opts.heavy)
	if err != nil {
		return nil, err
	}
	defer releaseSlot()

	runCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	out, err := exec.CommandContext(runCtx, CONVERT_BIN, imageMagickConvertArgs(lowMemory, args...)...).CombinedOutput()
	if err != nil && runCtx.Err() != nil {
		return out, runCtx.Err()
	}
	return out, err
}

func processWasKilled(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && status.Signal() == syscall.SIGKILL
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func sameStringSlice(a []string, b []string) bool {
	return strings.Join(a, "\x00") == strings.Join(b, "\x00")
}
