package msbimport

import (
	"context"
	"strings"

	log "github.com/sirupsen/logrus"
)

// Replaces tgs to gif.
func RlottieToGIF(f string) (string, error) {
	release, err := acquireLottieGIFSlot(context.Background(), nil)
	if err != nil {
		return "", err
	}
	defer release()

	bin := "msb_rlottie.py"
	fOut := strings.ReplaceAll(f, ".tgs", ".gif")
	args := []string{f, fOut}
	out, err := commandOutputWithTimeout(bin, args...)
	if err != nil {
		log.Errorln("lottieToGIF ERROR!", string(out))
		return "", err
	}
	//Optimize GIF
	commandOutputWithTimeout("gifsicle", "--batch", "-O2", "--lossy=60", fOut)
	return fOut, nil
}
