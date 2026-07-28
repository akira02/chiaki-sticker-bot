package msbimport

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

var ErrNoStickerFound = errors.New("no sticker found in import source")
var ErrStickerTooLarge = errors.New("sticker too large for Telegram after compression")

// This function serves as an entrypoint for this package.
// Parse a LINE or Kakao link and fetch metadata.
// The metadata (which means the LineData struct) can be used to call prepareImportStickers.
// Returns a string and an error. String act as a warning message, empty string means no warning yield.
//
// Attention: After this function returns, ld.Amount, ld.Files will NOT be available!
func ParseImportLink(link string, ld *LineData) (string, error) {
	var warn string

	u, err := url.Parse(link)
	if err != nil {
		return warn, err
	}
	switch {
	case strings.HasSuffix(u.Host, "line.me"):
		ld.Store = "line"
		return parseLineLink(link, ld)
	case strings.HasSuffix(u.Host, "kakao.com"):
		ld.Store = "kakao"
		return parseKakaoLink(link, ld)
	default:
		return warn, errors.New("unknow import")
	}
}

// Prepare stickers files.
// Should be called after calling ParseImportLink().
// A context is provided, which can be used to interrupt the process.
// Even if this function returns, file preparation might still in progress.
// LineData.Amount, LineData.Files will be produced after return.
// wg.Wait() is required for individual LineData.Files
//
// convertToTGFormat: Convert original stickers to Telegram sticker format.
// convertToTGEmoji: If present sticker set is Emoji(LINE), convert to 100x100 Telegram CustomEmoji.
func PrepareImportStickers(ctx context.Context, ld *LineData, workDir string, convertToTGFormat bool, convertToTGEmoji bool) error {
	switch ld.Store {
	case "line":
		return prepareLineStickers(ctx, ld, workDir, convertToTGFormat, convertToTGEmoji)
	case "kakao":
		return prepareKakaoStickers(ctx, ld, workDir, convertToTGFormat)
	}
	return nil
}

// Convert imported sticker to Telegram format,
// which means WEBM for animated and WEBP for static
// with 512x512 dimension.
func convertSToTGFormat(ctx context.Context, ld *LineData) {
	for i, s := range ld.Files {
		if s.Status == nil {
			s.Status = NewConversionStatus()
		}
		select {
		case <-ctx.Done():
			log.Warn("convertSToTGFormat received ctxDone!")
			// Mark remaining files (not yet submitted) with cancellation error.
			for j := i; j < len(ld.Files); j++ {
				ld.Files[j].CError = ctx.Err()
				ld.Files[j].Wg.Done()
			}
			return
		default:
		}
		var err error
		s.Ctx = ctx
		// If lineS is animated, commit to worker pool
		// since encoding vp9 is time and resource costy.
		if ld.IsAnimated {
			if err := wpConvertWebm.Invoke(s); err != nil {
				s.CError = err
				s.Wg.Done()
			}
		} else {
			// Info level: without this a stalled conversion leaves no trace at all
			// in production logs, which run at info.
			log.Infof("convert: static [%d/%d] start %s", i+1, len(ld.Files), filepath.Base(s.OriginalFile))
			started := time.Now()
			s.ConvertedFile, err = IMToWebpTGStaticContext(ctx, s.OriginalFile, s.ConvertToEmoji)
			elapsed := time.Since(started).Truncate(time.Millisecond)
			if err != nil {
				if ctx.Err() != nil {
					s.CError = ctx.Err()
				} else {
					s.CError = err
				}
				log.Warnf("convert: static [%d/%d] failed after %s: %v", i+1, len(ld.Files), elapsed, s.CError)
			} else {
				log.Infof("convert: static [%d/%d] done in %s", i+1, len(ld.Files), elapsed)
			}
			s.Wg.Done()
		}
	}
}
