package msbimport

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"
)

// IMToGif converts an animated WebP (no extension) to GIF using ImageMagick.
// GIF is palette-based (8-bit) so decoded frame memory is ~4x smaller than
// APNG (RGBA), making it more suitable for memory-constrained environments.
func IMToGif(f string) (string, error) {
	pathOut := f + ".gif"
	// -coalesce ensures proper frame disposal before palette reduction.
	args := []string{"WEBP:" + f, "-coalesce", pathOut}

	out, err := runImageMagickConvertHeavyWithOOMRetry(context.Background(), convertCommandTimeout(), nil, args...)
	if err != nil {
		log.Warnln("IMToGif ERROR:", string(out))
		return "", err
	}
	if st, stErr := os.Stat(pathOut); stErr != nil || st.Size() == 0 {
		log.Warnln("IMToGif: output file missing or empty:", string(out))
		return "", errors.New("IMToGif: output file missing or empty")
	} else {
		log.Infof("IMToGif: OK, %d bytes -> %s", st.Size(), pathOut)
	}
	return pathOut, nil
}

func IMToApng(f string) (string, error) {
	pathOut := f + ".apng"
	// Use "WEBP:" prefix so ImageMagick detects the format even without a file extension.
	args := []string{"WEBP:" + f, pathOut}

	out, err := runImageMagickConvertHeavyWithOOMRetry(context.Background(), convertCommandTimeout(), nil, args...)
	if err != nil {
		log.Warnln("imToApng ERROR:", string(out))
		return "", err
	}
	// Detect silent failure: ImageMagick may exit 0 but produce an empty file.
	if st, stErr := os.Stat(pathOut); stErr != nil || st.Size() == 0 {
		log.Warnln("imToApng: output file missing or empty, ImageMagick output:", string(out))
		return "", errors.New("imToApng: output file missing or empty")
	}
	return pathOut, nil
}

// If the source is IMAGE, convert to WEBP,
// If the source is VIDEO, convert to WEBM
func ConverMediaToTGStickerSmart(f string, isCustomEmoji bool) (string, error) {
	// ImageMagick's identify is reliable for images, but video support depends on
	// optional delegates and can fail before ffmpeg ever gets a chance to encode
	// the sticker. Route known video containers directly to the video pipeline.
	if isVideoMediaFile(f) {
		return FFToWebmTGVideo(f, isCustomEmoji)
	}

	// Count frames by running identify without -format and counting output lines.
	// This is more reliable than -format "%n" for animated WebP, which older
	// ImageMagick versions may misreport as 1 frame.
	identifyOut, err := commandOutputWithTimeout(IDENTIFY_BIN, append(IDENTIFY_ARGS, f)...)
	if err != nil {
		log.Warnln("ConverMediaToTGStickerSmart identify ERROR:", string(identifyOut))
		return "", err
	}

	lines := strings.Split(strings.TrimSpace(string(identifyOut)), "\n")
	frameCount := len(lines)

	if frameCount == 0 {
		log.Warnln("ConverMediaToTGStickerSmart ERROR: Frame count is zero.")
		return "", errors.New("frame count is zero")
	}

	log.Debugf("ConverMediaToTGStickerSmart: %s frameCount=%d", f, frameCount)

	if frameCount > 1 {
		return FFToWebmTGVideo(f, isCustomEmoji)
	}
	return IMToWebpTGStatic(f, isCustomEmoji)
}

func isVideoMediaFile(f string) bool {
	switch strings.ToLower(filepath.Ext(f)) {
	case ".mp4", ".mov", ".webm":
		return true
	default:
		return false
	}
}

// isAnimatedWebp reports whether f is an animated WebP by inspecting the
// container header. Animated files use the extended VP8X chunk with the
// animation flag (0x02) set. ffmpeg's native webp decoder cannot decode these
// (it skips the ANIM/ANMF chunks), so callers must convert via APNG first.
func isAnimatedWebp(f string) bool {
	file, err := os.Open(f)
	if err != nil {
		return false
	}
	defer file.Close()

	var hdr [21]byte
	if _, err := io.ReadFull(file, hdr[:]); err != nil {
		return false
	}
	if string(hdr[0:4]) != "RIFF" || string(hdr[8:12]) != "WEBP" {
		return false
	}
	// VP8X (extended format) is required for animation; flags byte is at offset 20.
	if string(hdr[12:16]) != "VP8X" {
		return false
	}
	return hdr[20]&0x02 != 0
}

func IMStackToWebp(base string, overlay string) (string, error) {
	fOut := base + ".composite.webp"

	args := []string{
		base, overlay, "-background", "none", "-filter", "Lanczos", "-resize", "512x512", "-composite",
		"-define", "webp:lossless=true", fOut,
	}
	out, err := runImageMagickConvertWithOOMRetry(context.Background(), convertCommandTimeout(), args...)
	if err != nil {
		log.Errorln("IM stack ERROR!", string(out))
		return "", err
	} else {
		return fOut, nil
	}
}

// Replaces .webm or .webp to .png
func IMToPNGThumb(f string) error {
	pathOut := strings.ReplaceAll(f, ".webm", ".png")
	pathOut = strings.ReplaceAll(pathOut, ".webp", ".png")

	if strings.HasSuffix(f, ".webm") {
		tempThumb := f + ".thumb.png"
		FFtoPNG(f, tempThumb)
		f = tempThumb
	}

	args := []string{
		f + "[0]", "-background", "none", "-alpha", "on",
		"-resize", "96x96",
		"-gravity", "center", "-extent", "96x96",
		pathOut,
	}

	out, err := runImageMagickConvertWithOOMRetry(context.Background(), convertCommandTimeout(), args...)
	if err != nil {
		log.Warnln("imToPng ERROR:", string(out))
		return err
	}
	return err
}
