package msbimport

import (
	"context"
	"path/filepath"
	"time"

	"github.com/panjf2000/ants/v2"
	log "github.com/sirupsen/logrus"
)

// Workers pool for converting webm
var wpConvertWebm, _ = ants.NewPoolWithFunc(1, wConvertWebm)

// Accepts *LineFile
func wConvertWebm(i interface{}) {
	lf := i.(*LineFile)
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
