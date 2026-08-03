package msbimport

import (
	"context"
	"path/filepath"
	"time"

	log "github.com/sirupsen/logrus"
)

// webmWorkerQueue serializes animated conversion setup. Unlike the former ants
// pool, it reports FIFO positions so work waiting before heavyQueue never looks
// like it has already started converting.
var webmWorkerQueue = &converterQueue{name: "webm conversion worker", capacity: 1}

// wConvertWebm runs one animated conversion after its dispatch slot is granted.
// The encoder itself remains separately serialized by heavyQueue.
func wConvertWebm(lf *LineFile) {
	defer lf.Wg.Done()

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
	release, err := webmWorkerQueue.acquire(ctx, lf.Status)
	if err != nil {
		lf.CError = err
		return
	}
	defer release()

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
