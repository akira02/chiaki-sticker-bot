package core

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/star-39/moe-sticker-bot/pkg/msbimport"
	tele "gopkg.in/telebot.v3"
)

var errNoStickerAvailable = errors.New("no sticker available")

const telegramStickerAPIAttempts = 3
const maxCustomEmojiPerStickerSet = 200

//TODO: Shrink oversized function.

// Final stage of automated sticker submission.
// Automated means all emojis are same.
func submitStickerSetAuto(createSet bool, c tele.Context) error {
	ud := udFromCtx(c)
	pText, teleMsg, _ := sendProcessStarted(ud, c, "Waiting...")
	if err := waitImportPreparation(ud, pText, teleMsg, c); err != nil {
		return err
	}
	if err := sessionContextErr(ud); err != nil {
		return err
	}

	if len(ud.stickerData.stickers) == 0 {
		log.Error("No sticker to commit!")
		return errNoStickerAvailable
	}

	log.Debugln("stickerData summary:")
	log.Debugln(ud.stickerData)
	errorCount := 0
	flCount := &ud.stickerData.flCount
	ssName := ud.stickerData.id
	ssTitle := ud.stickerData.title
	ssType := ud.stickerData.stickerSetType

	//Set emojis and keywords in batch.
	for _, s := range ud.stickerData.stickers {
		s.emojis = ud.stickerData.emojis
		s.keywords = MSB_DEFAULT_STICKER_KEYWORDS
	}

	// Surface conversion progress. The sf.wg.Wait() barriers inside the commit
	// path below would otherwise stay silent for minutes while animated stickers
	// transcode, making the bot look stuck on "Preparing stickers".
	convTotal := len(ud.stickerData.stickers)
	lastConvProgressEdit := time.Time{}
	for i, sf := range ud.stickerData.stickers {
		if err := sessionContextErr(ud); err != nil {
			return err
		}
		lastConvProgressEdit = waitStickerConversionProgress(sf, i, convTotal, lastConvProgressEdit, pText, teleMsg, c)
	}

	batches := splitStickerSetBatches(ud.stickerData.stickers, createSet, ssType)
	createdPacks := make([]stickerSetPack, 0, len(batches))
	for part, stickers := range batches {
		name, title := ssName, ssTitle
		if len(batches) > 1 {
			name = stickerSetPartName(ssName, part+1)
			title = stickerSetPartTitle(ssTitle, part+1, len(batches))
		}
		errors, err := commitStickerSetAutoBatch(ud, createSet, stickers, part*maxCustomEmojiPerStickerSet, c, name, title, ssType, pText, teleMsg, flCount)
		if err != nil {
			return err
		}
		errorCount += errors
		createdPacks = append(createdPacks, stickerSetPack{name: name, title: title})
	}

	// Tolerate at most 3 errors when importing sticker set.
	if ud.command == "import" && errorCount > 3 {
		return errors.New("too many errors importing")
	}

	if createSet {
		for _, pack := range createdPacks {
			if ud.command == "import" {
				insertLineS(ud.lineData.Id, ud.lineData.Link, pack.name, pack.title, true)
			}
			insertUserS(c.Sender().ID, pack.name, pack.title, time.Now().Unix())
		}
		if ud.command == "import" {
			// Only verify for import.
			// User generated sticker set might intentionally contain same stickers.
			if *flCount > 1 {
				verifyFloodedStickerSet(c, *flCount, errorCount, ud.lineData.Amount, ud.stickerData.id)
			}
		}
	}
	editProgressMsg(0, 0, "Success! /start", pText, teleMsg, c)
	c.Send("If you like this bot, please give us a ⭐️\n如果你喜歡這個 Bot，請幫我們按個 ⭐️\nhttps://github.com/akira02/chiaki-sticker-bot")
	for _, pack := range createdPacks {
		sendSFromSS(c, pack.name, teleMsg)
	}
	return nil
}

type stickerSetPack struct {
	name  string
	title string
}

func splitStickerSetBatches(stickers []*StickerFile, createSet bool, ssType string) [][]*StickerFile {
	if !createSet || ssType != tele.StickerCustomEmoji || len(stickers) <= maxCustomEmojiPerStickerSet {
		return [][]*StickerFile{stickers}
	}
	var batches [][]*StickerFile
	for len(stickers) > 0 {
		end := maxCustomEmojiPerStickerSet
		if end > len(stickers) {
			end = len(stickers)
		}
		batches = append(batches, stickers[:end])
		stickers = stickers[end:]
	}
	return batches
}

func stickerSetPartName(name string, part int) string {
	if part == 1 {
		return name
	}
	suffix := "_part" + strconv.Itoa(part)
	marker := "_by_"
	index := strings.LastIndex(name, marker)
	if index == -1 {
		return name + suffix
	}
	prefix, owner := name[:index], name[index:]
	maxPrefixLength := 64 - len(suffix) - len(owner)
	if len(prefix) > maxPrefixLength {
		prefix = prefix[:maxPrefixLength]
	}
	return prefix + suffix + owner
}

func stickerSetPartTitle(title string, part int, total int) string {
	if total == 1 {
		return title
	}
	suffix := fmt.Sprintf(" (%d/%d)", part, total)
	maxTitleRunes := 128 - len([]rune(suffix))
	runes := []rune(title)
	if len(runes) > maxTitleRunes {
		runes = runes[:maxTitleRunes]
	}
	return string(runes) + suffix
}

func commitStickerSetAutoBatch(ud *UserData, createSet bool, stickers []*StickerFile, offset int, c tele.Context, ssName string, ssTitle string, ssType string, pText string, teleMsg *tele.Message, flCount *int) (int, error) {
	batchCreateSuccess := false
	if createSet {
		err := createStickerSetBatch(ud.ctx, stickers, c, ssName, ssTitle, ssType)
		if err != nil {
			log.Warnln("sticker.go: Error batch create:", sanitizeErrorText(err))
			existingCount, exists := existingStickerSetCount(c, ssName)
			expectedBatchCount := batchCreateExpectedCount(len(stickers))
			if exists && existingCount >= expectedBatchCount {
				batchCreateSuccess = true
			} else if exists && existingCount > 0 {
				if err := deleteStickerSet(c, ssName); err != nil {
					return 0, fmt.Errorf("batch create partially created sticker set %s with %d/%d stickers, and cleanup failed: %w", ssName, existingCount, expectedBatchCount, err)
				}
			}
		} else {
			batchCreateSuccess = true
		}
	}

	errorCount := 0
	for index, sf := range stickers {
		if err := sessionContextErr(ud); err != nil {
			return errorCount, err
		}
		if batchCreateSuccess && index < batchCreateExpectedCount(len(stickers)) {
			continue
		}
		if createSet && index == 0 {
			if err := createStickerSet(false, sf, c, ssName, ssTitle, ssType); err != nil {
				return errorCount, err
			}
			continue
		}

		go editProgressMsg(offset+index, len(ud.stickerData.stickers), "", pText, teleMsg, c)
		if err := commitSingleticker(offset+index, flCount, false, sf, c, ud.stickerData, ssName, ssType); err != nil {
			log.Warnln("execAutoCommit: a sticker failed to add.", err)
			sendOneStickerFailedToAdd(c, offset+index, err)
			errorCount++
		}
		if *flCount == 1 {
			time.Sleep(time.Duration(10+rand.Intn(10)) * time.Second)
		} else if *flCount > 1 {
			time.Sleep(time.Duration(60+rand.Intn(10)) * time.Second)
		}
	}
	return errorCount, nil
}

func sessionContextErr(ud *UserData) error {
	if ud == nil || ud.ctx == nil {
		return nil
	}
	return ud.ctx.Err()
}

func waitStickerConversionProgress(sf *StickerFile, index int, total int, lastEdit time.Time, pText string, teleMsg *tele.Message, c tele.Context) time.Time {
	done := make(chan struct{})
	go func() {
		sf.wg.Wait()
		close(done)
	}()

	firstNotice := time.NewTimer(3 * time.Second)
	heartbeat := time.NewTicker(20 * time.Second)
	defer firstNotice.Stop()
	defer heartbeat.Stop()

	edit := func(doneCount int, status string, force bool) {
		now := time.Now()
		if !force && now.Sub(lastEdit) < 3*time.Second {
			return
		}
		prog := conversionProgressText(doneCount, total, status)
		editProgressMsg(0, 0, prog, pText, teleMsg, c)
		lastEdit = now
	}

	// Even with no per-sticker status, refresh the message so a slow first
	// conversion doesn't leave the stale "Processing files" text on screen.
	editStatus := func() {
		edit(index, sf.conversionStatus.Message(), true)
	}

	for {
		select {
		case <-done:
			doneCount := index + 1
			force := doneCount == total
			// Always attempt an update; edit() throttles to ~once per 3s, so this
			// stays well under Telegram's edit rate limit while showing the true
			// running count instead of coarse 25% jumps.
			edit(doneCount, "", force)
			return lastEdit
		case <-firstNotice.C:
			editStatus()
		case <-heartbeat.C:
			editStatus()
		}
	}
}

func conversionProgressText(done int, total int, status string) string {
	prog := "<code>Converting / 轉檔中...\n       " + strconv.Itoa(done) + " of " + strconv.Itoa(total)
	if status != "" {
		prog += "\nSticker " + strconv.Itoa(done+1) + " " + status
	}
	return prog + "</code>"
}

// Only fatal error should be returned.
func submitStickerManual(createSet bool, pos int, emojis []string, keywords []string, c tele.Context) error {
	ud := udFromCtx(c)
	var err error
	name := ud.stickerData.id
	title := ud.stickerData.title
	ssType := ud.stickerData.stickerSetType

	if len(ud.stickerData.stickers) == 0 {
		log.Error("No sticker to commit!!")
		return errNoStickerAvailable
	}
	if pos < 0 || pos >= len(ud.stickerData.stickers) {
		log.Errorf("No sticker to commit at pos %d, total %d", pos, len(ud.stickerData.stickers))
		return errNoStickerAvailable
	}

	sf := ud.stickerData.stickers[pos]
	sf.emojis = emojis
	sf.keywords = keywords

	//Do not submit to goroutine when creating sticker set.
	if createSet && pos == 0 {
		defer close(ud.commitChans[pos])
		err = createStickerSet(false, sf, c, name, title, ssType)
		if err != nil {
			log.Errorln("create failed. ", err)
			return err
		} else {
			ud.stickerData.cAmount += 1
		}
		if ud.stickerData.lAmount == 1 {
			return finalizeSubmitStickerManual(c, createSet, ud)
		}
	} else {
		go func() {
			//wait for the previous commit to be done.
			if pos > 0 {
				<-ud.commitChans[pos-1]
			}

			err = commitSingleticker(pos, &ud.stickerData.flCount, false, sf, c, ud.stickerData, name, ssType)
			if err != nil {
				sendOneStickerFailedToAdd(c, pos, err)
				log.Warnln("execEmojiAssign: a sticker failed to add: ", err)
			} else {
				ud.stickerData.cAmount += 1
			}

			if pos+1 == ud.stickerData.lAmount {
				finalizeSubmitStickerManual(c, createSet, ud)
			}
			close(ud.commitChans[pos])
		}()
	}
	return nil
}

func finalizeSubmitStickerManual(c tele.Context, createSet bool, ud *UserData) error {
	if createSet {
		if ud.command == "import" {
			insertLineS(ud.lineData.Id, ud.lineData.Link, ud.stickerData.id, ud.stickerData.title, false)
		}
		insertUserS(c.Sender().ID, ud.stickerData.id, ud.stickerData.title, time.Now().Unix())
	}
	sendExecEmojiAssignFinished(c)
	// c.Send("Success! /start")
	sendSFromSS(c, ud.stickerData.id, nil)
	endSession(c)
	return nil
}

// safeModeInput returns the best file to re-encode for safe mode.
// Prefer the original source so transparent APNG/PNG inputs keep their alpha
// instead of relying on ffmpeg to decode alpha from an already-encoded WebM.
// TGS still needs the converted file because ffmpeg cannot read it directly.
func safeModeInput(sf *StickerFile) string {
	if sf.oPath != "" && !strings.EqualFold(filepath.Ext(sf.oPath), ".tgs") {
		return sf.oPath
	}
	return sf.cPath
}

func validateStickerInput(sf *StickerFile, file string) error {
	if sf.cError != nil {
		return sf.cError
	}
	if sf.fileID != "" {
		return nil
	}
	if file == "" {
		return errors.New("converted sticker file path is empty")
	}
	if _, err := os.Stat(file); err != nil {
		return err
	}
	return nil
}

func isTelegramTemporaryServerError(err error) bool {
	if err == nil {
		return false
	}
	errText := strings.ToLower(err.Error())
	return strings.Contains(errText, "gateway timeout") ||
		strings.Contains(errText, "bad gateway") ||
		strings.Contains(errText, "service unavailable") ||
		strings.Contains(errText, "(502)") ||
		strings.Contains(errText, "(503)") ||
		strings.Contains(errText, "(504)")
}

func isRetryableTelegramWriteError(err error) bool {
	return isTimeoutError(err) || isTelegramTemporaryServerError(err)
}

func telegramStickerRetryDelay(attempt int) time.Duration {
	return time.Duration(5+attempt*10) * time.Second
}

// sendDocumentWithRetry uploads a document, retrying on transient Telegram
// timeouts/5xx. Large whole-pack zips upload slowly from the constrained VM, so a
// single timed-out attempt should not fail the whole download.
func sendDocumentWithRetry(c tele.Context, doc *tele.Document) error {
	var lastErr error
	for i := 0; i < telegramStickerAPIAttempts; i++ {
		_, err := c.Bot().Send(c.Recipient(), doc)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isRetryableTelegramWriteError(err) {
			return err
		}
		if i == telegramStickerAPIAttempts-1 {
			break
		}
		sleepTime := telegramStickerRetryDelay(i)
		log.Warnf("sendDocumentWithRetry: retryable Telegram error, sleeping %s (attempt %d/%d): %s", sleepTime, i+1, telegramStickerAPIAttempts, sanitizeErrorText(err))
		time.Sleep(sleepTime)
	}
	return fmt.Errorf("sendDocumentWithRetry: exceeded retry limit: %w", lastErr)
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if ctx == nil {
		time.Sleep(d)
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Create sticker set if needed.
func createStickerSet(safeMode bool, sf *StickerFile, c tele.Context, name string, title string, ssType string) error {
	var file string
	var isCustomEmoji bool
	if ssType == tele.StickerCustomEmoji {
		isCustomEmoji = true
	}

	sf.wg.Wait()

	if safeMode {
		var err error
		file, err = msbimport.FFToWebmSafe(safeModeInput(sf), isCustomEmoji)
		if err != nil {
			return err
		}
	} else {
		file = sf.cPath
	}
	if err := validateStickerInput(sf, file); err != nil {
		return err
	}

	log.Debugln("createStickerSet: attempting, sticker file path:", sf.cPath)

	input := tele.InputSticker{
		Emojis:   sf.emojis,
		Keywords: sf.keywords,
	}
	if sf.fileID != "" {
		input.Sticker = sf.fileID
		input.Format = sf.format
	} else {
		input.Sticker = "file://" + file
		input.Format = guessInputStickerFormat(file)
	}

	var floodErr tele.FloodError
	var lastErr error
	for i := 0; i < telegramStickerAPIAttempts; i++ {
		err := c.Bot().CreateStickerSet(c.Recipient(), []tele.InputSticker{input}, name, title, ssType)
		if err == nil {
			return nil
		}
		lastErr = err

		log.Errorf("createStickerSet error:%s for set:%s.", sanitizeErrorText(err), name)

		if errors.As(err, &floodErr) {
			sleepSec := floodErr.RetryAfter
			if sleepSec > 120 {
				log.Warnf("createStickerSet: RA=%ds too long, capping at 120s.", sleepSec)
				sleepSec = 120
			}
			log.Warnf("createStickerSet: flood limit, sleeping %ds (attempt %d/%d).", sleepSec, i+1, telegramStickerAPIAttempts)
			time.Sleep(time.Duration(sleepSec) * time.Second)
			continue
		} else if isRetryableTelegramWriteError(err) {
			sleepTime := telegramStickerRetryDelay(i)
			log.Warnf("createStickerSet: retryable Telegram error, sleeping %s (attempt %d/%d): %s", sleepTime, i+1, telegramStickerAPIAttempts, sanitizeErrorText(err))
			time.Sleep(sleepTime)
			continue
		} else if strings.Contains(strings.ToLower(err.Error()), "video_long") {
			if safeMode {
				log.Error("safe mode DID NOT resolve video_long problem.")
				return err
			}
			log.Warnln("returned video_long, attempting safe mode.")
			return createStickerSet(true, sf, c, name, title, ssType)
		} else if reconcileOccupiedBatchCreate(c, err, name, 1) {
			log.Warnln("createStickerSet returned SHORTNAME_OCCUPY_FAILED, but sticker set exists; treating create as success.")
			return nil
		} else {
			return err
		}
	}
	return fmt.Errorf("createStickerSet: exceeded retry limit: %w", lastErr)
}

func reconcileOccupiedBatchCreate(c tele.Context, createErr error, name string, stickerCount int) bool {
	if createErr == nil || !strings.Contains(strings.ToLower(createErr.Error()), "shortname_occupy_failed") {
		return false
	}
	return reconcileCreatedStickerSet(c, name, stickerCount)
}

func reconcileCreatedStickerSet(c tele.Context, name string, stickerCount int) bool {
	if stickerCount <= 0 {
		return false
	}

	expectedCount := batchCreateExpectedCount(stickerCount)

	for i := 0; i < 5; i++ {
		gotCount, ok := existingStickerSetCount(c, name)
		if ok {
			if gotCount >= expectedCount {
				log.Warnf("reconcileCreatedStickerSet: found existing set:%s with %d stickers, expected at least %d.", name, gotCount, expectedCount)
				return true
			}
			log.Warnf("reconcileCreatedStickerSet: found existing set:%s, but sticker count is %d, expected at least %d.", name, gotCount, expectedCount)
			return false
		}
		if i < 4 {
			time.Sleep(2 * time.Second)
		}
	}

	return false
}

func existingStickerSetCount(c tele.Context, name string) (int, bool) {
	ss, err := c.Bot().StickerSet(name)
	if err == nil {
		return len(ss.Stickers), true
	}
	if isRetryableTelegramWriteError(err) {
		log.Warnf("existingStickerSetCount: StickerSet lookup failed, retrying: %s", sanitizeErrorText(err))
	}
	return 0, false
}

func batchCreateExpectedCount(stickerCount int) int {
	if stickerCount > 50 {
		return 50
	}
	return stickerCount
}

func deleteStickerSet(c tele.Context, name string) error {
	var lastErr error
	for i := 0; i < telegramStickerAPIAttempts; i++ {
		_, err := c.Bot().Raw("deleteStickerSet", map[string]string{"name": name})
		if err == nil {
			log.Warnf("deleteStickerSet: deleted partial sticker set:%s.", name)
			return nil
		}
		lastErr = err
		if !isRetryableTelegramWriteError(err) {
			return err
		}
		if i == telegramStickerAPIAttempts-1 {
			break
		}
		sleepTime := telegramStickerRetryDelay(i)
		log.Warnf("deleteStickerSet: retryable Telegram error, sleeping %s (attempt %d/%d): %s", sleepTime, i+1, telegramStickerAPIAttempts, sanitizeErrorText(err))
		time.Sleep(sleepTime)
	}
	return fmt.Errorf("deleteStickerSet: exceeded retry limit: %w", lastErr)
}

// Create sticker set with multiple StickerFile.
// API 7.2 feature, consider it experimental.
// If it still fails after retry, return error and let caller try conventional way.
func createStickerSetBatch(ctx context.Context, sfs []*StickerFile, c tele.Context, name string, title string, ssType string) error {
	var inputs []tele.InputSticker
	log.Debugln("createStickerSetBatch: attempting, batch creation:", name)

	for i, sf := range sfs {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		sf.wg.Wait()
		file := sf.cPath
		if err := validateStickerInput(sf, file); err != nil {
			return err
		}
		input := tele.InputSticker{
			Emojis:   sf.emojis,
			Keywords: sf.keywords,
		}
		if sf.fileID != "" {
			input.Sticker = sf.fileID
			input.Format = sf.format
		} else {
			input.Sticker = "file://" + file
			input.Format = guessInputStickerFormat(file)
		}
		inputs = append(inputs, input)

		//Up to 50 stickers.
		if i == 49 {
			break
		}
	}

	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	var lastErr error
	for i := 0; i < telegramStickerAPIAttempts; i++ {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		err := c.Bot().CreateStickerSet(c.Recipient(), inputs, name, title, ssType)
		if err == nil {
			return nil
		}
		lastErr = err
		if reconcileCreatedStickerSet(c, name, len(inputs)) {
			log.Warnf("createStickerSetBatch: create returned error, but set exists; treating as success: %s", sanitizeErrorText(err))
			return nil
		}
		if !isRetryableTelegramWriteError(err) {
			return err
		}
		if i == telegramStickerAPIAttempts-1 {
			break
		}
		sleepTime := telegramStickerRetryDelay(i)
		log.Warnf("createStickerSetBatch: retryable Telegram error, sleeping %s (attempt %d/%d): %s", sleepTime, i+1, telegramStickerAPIAttempts, sanitizeErrorText(err))
		if err := sleepWithContext(ctx, sleepTime); err != nil {
			return err
		}
	}
	return fmt.Errorf("createStickerSetBatch: exceeded retry limit: %w", lastErr)
}

// Commit single sticker, retry happens inside this function.
// If all retries failed, return err.
//
// flCount counts the total flood limit for entire sticker set.
// pos is for logging only.
// effectiveRecipient returns the sticker set owner as recipient when admin
// is managing another user's set, so Telegram API calls use the correct user_id.
func effectiveRecipient(c tele.Context, sd *StickerData) tele.Recipient {
	if sd != nil && sd.ownerUID != 0 && sd.ownerUID != c.Sender().ID {
		return &tele.User{ID: sd.ownerUID}
	}
	return c.Recipient()
}

func commitSingleticker(pos int, flCount *int, safeMode bool, sf *StickerFile, c tele.Context, sd *StickerData, name string, ssType string) error {
	var err error
	var floodErr tele.FloodError
	var file string
	var isCustomEmoji bool
	if ssType == tele.StickerCustomEmoji {
		isCustomEmoji = true
	}
	sf.wg.Wait()

	if safeMode {
		file, err = msbimport.FFToWebmSafe(safeModeInput(sf), isCustomEmoji)
		if err != nil {
			return err
		}
	} else {
		file = sf.cPath
	}
	if err := validateStickerInput(sf, file); err != nil {
		return err
	}

	log.Debugln("commitSingleticker: attempting, sticker file path:", sf.cPath)
	// Retry loop.
	// For each sticker, retry at most 2 times, means 3 commit attempts in total.
	for i := 0; i < 3; i++ {
		input := tele.InputSticker{
			Emojis:   sf.emojis,
			Keywords: sf.keywords,
		}
		if sf.fileID != "" {
			input.Sticker = sf.fileID
			input.Format = sf.format
		} else {
			input.Sticker = "file://" + file
			input.Format = guessInputStickerFormat(file)
		}

		err = c.Bot().AddSticker(effectiveRecipient(c, sd), input, name)
		if err == nil {
			// Telegram applies a separate, per-set flood limit to rapid
			// addStickerToSet calls independent of our overall API rate limit.
			// Pace ourselves proactively instead of only backing off after
			// already tripping it.
			time.Sleep(150 * time.Millisecond)
			return nil
		}

		log.Errorf("commit sticker error:%s for set:%s.", err, name)
		// This flood limit error only happens to a specific user at a specific time.
		// It is "fake" most of time, since TDLib in API Server will automatically retry.
		// However, API always return 429.
		// Since API side will always do retry at TDLib level, message_id was also being kept so
		// no position shift will happen.
		// Flood limit error could be probably ignored.
		if errors.As(err, &floodErr) {
			// This reflects the retry count for entire SS.
			*flCount += 1
			log.Warnf("commitSticker: Flood limit encountered for user:%d, set:%s, count:%d, pos:%d", c.Sender().ID, name, *flCount, pos)
			log.Warnln("commitSticker: commit sticker retry after: ", floodErr.RetryAfter)
			if *flCount == 2 {
				sendFLWarning(c)
			}

			//Sleep
			if floodErr.RetryAfter > 60 {
				log.Error("RA too long! Telegram's bug? Attempt to sleep for 120 seconds.")
				time.Sleep(120 * time.Second)
			} else {
				extraRA := *flCount * 15
				log.Warnf("Sleeping for %d seconds due to FL.", floodErr.RetryAfter+extraRA)
				time.Sleep(time.Duration(floodErr.RetryAfter+extraRA) * time.Second)
			}

			log.Warnf("Woken up from RA sleep. ignoring this error. user:%d, set:%s, count:%d, pos:%d", c.Sender().ID, name, *flCount, pos)

			//According to collected logs, exceeding 2 flood counts will sometimes cause api server to stop auto retrying.
			//Hence, we do retry here, else, break retry loop.
			if *flCount > 2 {
				continue
			} else {
				break
			}

		} else if strings.Contains(strings.ToLower(err.Error()), "video_long") {
			// Redo with safe mode on.
			// This should happen only one time.
			// So if safe mode is on and this error still occurs, return err.
			if safeMode {
				log.Error("safe mode DID NOT resolve video_long problem.")
				return err
			} else {
				log.Warnln("returned video_long, attempting safe mode.")
				return commitSingleticker(pos, flCount, true, sf, c, sd, name, ssType)
			}
		} else if strings.Contains(err.Error(), "invalid sticker emojis") {
			log.Warn("commitSticker: invalid emoji, resetting to a star emoji and retrying...")
			input.Emojis = []string{"⭐️"}
		} else if strings.Contains(err.Error(), "400") {
			// return remaining 400 BAD REQUEST immediately to parent without retry.
			return err
		} else {
			// Handle unknown error here.
			// We simply retry for 2 more times with 5 sec interval.
			log.Warnln("commitSticker: retrying... cause:", err)
			time.Sleep(5 * time.Second)
		}
	}

	log.Warn("commitSticker: too many retries")
	if errors.As(err, &floodErr) {
		log.Warn("commitSticker: reached max retry for flood limit, assume success.")
		return nil
	}
	return err
}

func editStickerEmoji(newEmojis []string, fid string, ud *UserData) error {
	return b.SetStickerEmojiList(ud.lastContext.Recipient(), fid, newEmojis)
}

// Receive and process user uploaded media file and convert to Telegram compliant format.
// Accept telebot Media and Sticker only.
func appendMedia(c tele.Context) error {
	log.Debugf("appendMedia: Received file, MType:%s, FileID:%s", c.Message().Media().MediaType(), c.Message().Media().MediaFile().FileID)
	var files []string
	var sfs []*StickerFile
	var err error
	var workDir string
	var savePath string

	ud := udFromCtx(c)
	ud.wg.Add(1)
	defer ud.wg.Done()
	ctx := ud.ctx

	if ud.stickerData.cAmount+len(ud.stickerData.stickers) > 120 {
		return errors.New("sticker set already full 此貼圖包已滿")
	}

	//Incoming media is a sticker.
	if c.Message().Sticker != nil && ((c.Message().Sticker.Type == tele.StickerCustomEmoji) == ud.stickerData.isCustomEmoji) {
		var format string
		if c.Message().Sticker.Video {
			format = "video"
		} else {
			format = "static"
		}
		sfs = append(sfs, &StickerFile{
			fileID: c.Message().Sticker.FileID,
			format: format,
		})
		log.Debugf("One received sticker file OK. ID:%s", c.Message().Sticker.FileID)
		goto CONTINUE
	}

	workDir = ud.workDir
	savePath = uploadedMediaSavePath(c.Message(), workDir, secHex(4))

	err = c.Bot().Download(c.Message().Media().MediaFile(), savePath)
	if err != nil {
		return fmt.Errorf("download media: %w", err)
	}

	if guessIsArchive(savePath) {
		files = append(files, stickerSourceFiles(msbimport.ArchiveExtract(savePath))...)
	} else {
		files = append(files, savePath)
	}
	if len(files) == 0 {
		return errors.New("archive did not contain any supported image or video files")
	}

	log.Debugln("appendMedia: Media downloaded to savepath:", savePath)
	for _, f := range files {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		var cf string
		var err error
		//If incoming media is already a sticker, use the file as is.
		if c.Message().Sticker != nil && ((c.Message().Sticker.Type == "custom_emoji") == ud.stickerData.isCustomEmoji) {
			cf = f
		} else {
			cf, err = msbimport.ConverMediaToTGStickerSmart(f, ud.stickerData.isCustomEmoji)
		}

		if err != nil {
			if ctx != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return ctxErr
				}
			}
			if _, statErr := os.Stat(f); os.IsNotExist(statErr) {
				log.Warnln("appendMedia: source sticker disappeared during conversion:", f)
				return errors.New("source sticker disappeared during conversion")
			}
			log.Warnln("Failed converting one user sticker", err)
			c.Send("Failed converting one user sticker:" + sanitizeErrorText(err))
			continue
		}
		sfs = append(sfs, &StickerFile{
			oPath: f,
			cPath: cf,
		})
		log.Debugf("One received file OK. oPath:%s | cPath:%s", f, cf)
	}

CONTINUE:
	if len(sfs) == 0 {
		return errors.New("download or convert error")
	}

	ud.stickerData.stickers = append(ud.stickerData.stickers, sfs...)
	ud.stickerData.lAmount = len(ud.stickerData.stickers)
	replySFileOK(c, len(ud.stickerData.stickers))
	return nil
}

func guessIsArchive(f string) bool {
	f = strings.ToLower(f)
	archiveExts := []string{".rar", ".7z", ".zip", ".tar", ".gz", ".bz2", ".zst", ".rar5"}
	for _, ext := range archiveExts {
		if strings.HasSuffix(f, ext) {
			return true
		}
	}
	return false
}

// stickerSourceFiles skips archive metadata such as .DS_Store and AppleDouble
// files. Those files are commonly included by macOS-created ZIPs but are not
// images, and passing them to ImageMagick makes an otherwise valid archive look
// like a conversion failure. Use the source filename extension here rather than
// byte sniffing: valid image files in ZIPs may be reported as generic binary
// data before ImageMagick decodes them.
func stickerSourceFiles(files []string) []string {
	sources := make([]string, 0, len(files))
	for _, file := range files {
		name := filepath.Base(file)
		if name == ".DS_Store" || strings.HasPrefix(name, "._") || strings.Contains(file, "__MACOSX") {
			log.Debugf("Skipping archive metadata file: %s", file)
			continue
		}

		if !isStickerSourceFile(name) {
			log.Debugf("Skipping unsupported archive file: %s", file)
			continue
		}
		sources = append(sources, file)
	}
	return sources
}

func isStickerSourceFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".apng", ".avif", ".bmp", ".gif", ".heic", ".heif", ".jpeg", ".jpg", ".png", ".tif", ".tiff", ".webp", ".webm", ".mp4", ".mov":
		return true
	default:
		return false
	}
}

// uploadedMediaSavePath preserves the source extension whenever Telegram
// provides one. Photos do not carry a filename, so give them a JPEG extension;
// image documents without a filename use their MIME type as a fallback. This
// keeps image decoders that rely on extensions compatible with PNG and other
// images sent as files.
func uploadedMediaSavePath(message *tele.Message, workDir, baseName string) string {
	path := filepath.Join(workDir, baseName)
	if message == nil {
		return path
	}

	switch {
	case message.Document != nil:
		if ext := filepath.Ext(message.Document.FileName); ext != "" {
			return path + ext
		}
		if extensions, err := mime.ExtensionsByType(message.Document.MIME); err == nil && len(extensions) > 0 {
			return path + extensions[0]
		}
	case message.Animation != nil:
		if ext := filepath.Ext(message.Animation.FileName); ext != "" {
			return path + ext
		}
	case message.Photo != nil:
		return path + ".jpg"
	}

	return path
}

func verifyFloodedStickerSet(c tele.Context, fc int, ec int, desiredAmount int, ssn string) {
	time.Sleep(31 * time.Second)
	ss, err := b.StickerSet(ssn)
	if err != nil {
		return
	}
	if desiredAmount < len(ss.Stickers) {
		log.Warnf("A flooded sticker set duplicated! floodCount:%d, errorCount:%d, ssn:%s, desired:%d, got:%d", fc, ec, ssn, desiredAmount, len(ss.Stickers))
		log.Warnf("Attempting dedup!")
		workdir := filepath.Join(dataDir, secHex(8))
		os.MkdirAll(workdir, 0755)
		for si, s := range ss.Stickers {
			if si > 0 {
				fp := filepath.Join(workdir, strconv.Itoa(si-1)+".webp")
				f := filepath.Join(workdir, strconv.Itoa(si)+".webp")
				c.Bot().Download(&s.File, f)

				if compCRC32(f, fp) {
					b.DeleteSticker(s.FileID)
				}
			}
		}
		os.RemoveAll(workdir)
	} else if desiredAmount > len(ss.Stickers) {
		log.Warnf("A flooded sticker set missing sticker! floodCount:%d, errorCount:%d, ssn:%s, desired:%d, got:%d", fc, ec, ssn, desiredAmount, len(ss.Stickers))
		c.Reply("Sorry, this sticker set seems corrupted, please check.\n抱歉, 這個貼圖包似乎有缺失貼圖, 請檢查一下.")
	} else {
		log.Infof("A flooded sticker set seems ok. floodCount:%d, errorCount:%d, ssn:%s, desired:%d, got:%d", fc, ec, ssn, desiredAmount, len(ss.Stickers))
	}
}
