package msbimport

import (
	"context"
	"path/filepath"
	"time"

	log "github.com/sirupsen/logrus"
)

// wConvertWebm runs one animated conversion. Expensive work is serialized by
// heavyQueue inside the converter, which also reports its FIFO position. A
// second worker pool here would create an unreported queue before heavyQueue.
func wConvertWebm(lf *LineFile) {
	defer lf.Wg.Done()
	log.Debugln("Converting in pool for:", lf)

	var err error
	ctx := lf.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		lf.CError = ctx.Err()
		return
	default:
	}
	// Info level: production logs run at info, so a stuck encode must be visible.
	log.Infof("convert: webm start %s", filepath.Base(lf.OriginalFile))
	started := time.Now()
	lf.ConvertedFile, err = FFToWebmTGVideoContextWithStatus(ctx, lf.OriginalFile, lf.ConvertToEmoji, lf.Status)
	elapsed := time.Since(started).Truncate(time.Millisecond)
	if err != nil {
		lf.CError = err
		log.Warnf("convert: webm %s failed after %s: %v", filepath.Base(lf.OriginalFile), elapsed, err)
		return
	}
	log.Infof("convert: webm done in %s -> %s", elapsed, filepath.Base(lf.ConvertedFile))
}
