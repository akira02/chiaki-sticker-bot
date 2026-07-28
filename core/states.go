package core

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/star-39/moe-sticker-bot/pkg/msbimport"
	tele "gopkg.in/telebot.v3"
)

// Handle conversation state during a command.
func handleMessage(c tele.Context) error {
	if shuttingDown.Load() {
		return c.Send(shutdownRetryMessage)
	}

	var err error
	command, state := getState(c)
	if command == "" {
		return handleNoSession(c)
	}
	switch command {
	case "import":
		switch state {
		case "waitSTitle":
			err = waitSTitle(c)
		case "waitEmojiChoice":
			err = waitEmojiChoice(c)
		case "process":
			err = stateProcessing(c)
		case "waitSEmojiAssign":
			err = waitSEmojiAssign(c)
		}
	case "create":
		switch state {
		case "waitSType":
			err = waitSType(c)
		case "waitSTitle":
			err = waitSTitle(c)
		case "waitSID":
			err = waitSID(c)
		case "waitSFile":
			err = waitSFile(c)
		case "waitEmojiChoice":
			err = waitEmojiChoice(c)
		case "waitSEmojiAssign":
			err = waitSEmojiAssign(c)
		case "process":
			err = stateProcessing(c)
		}
	case "manage":
		switch state {
		case "waitSManage":
			err = waitSManage(c)
		case "waitCbEditChoice":
			err = waitCbEditChoice(c)
		case "waitSFile":
			err = waitSFile(c)
		case "waitEmojiChoice":
			err = waitEmojiChoice(c)
		case "waitSEmojiAssign":
			err = waitSEmojiAssign(c)
		case "waitSTitle":
			err = waitSTitle(c)
		case "waitSDel":
			err = waitSDel(c)
		case "waitCbDelset":
			err = waitCbDelset(c)
		case "process":
			err = stateProcessing(c)
		}
	case "search":
		switch state {
		case "waitSearchKW":
			err = waitSearchKeyword(c)
		}
	case "getfid":
		err = cmdGetFID(c)
	}
	return err
}

// Received bare message without using a command.
func handleNoSession(c tele.Context) error {
	log.Debugf("user %d entered no session with message: %s", c.Sender().ID, c.Message().Text)

	//During previous stage, bot will reply to a message with callback buttons.
	//Now we react to user's choice.
	if c.Callback() != nil && c.Message().ReplyTo != nil {
		switch c.Callback().Data {
		case CB_DN_SINGLE:
			return downloadStickersAndSend(c.Message().ReplyTo.Sticker, "", c)
		case CB_DN_WHOLE:
			id := getSIDFromMessage(c.Message().ReplyTo)
			return downloadStickersAndSend(nil, id, c)
		case CB_MANAGE:
			return statePrepareSManage(c)
		case CB_ADMIN_MANAGE:
			return enterAdminManage(c)
		case CB_OK_IMPORT:
			return confirmImport(c, false)
		case CB_OK_IMPORT_EMOJI:
			return confirmImport(c, true)
		case CB_OK_DN:
			ud := initUserData(c, "download", "process")
			c.Send("Please wait...")
			msbimport.ParseImportLink(findLink(c.Message().ReplyTo.Text), ud.lineData)
			return downloadLineSToZip(c, ud)
		case CB_EXPORT_WA:
			hex := secHex(6)
			id := getSIDFromMessage(c.Message().ReplyTo)
			ss, _ := c.Bot().StickerSet(id)
			go prepareWebAppExportStickers(ss, hex)
			return sendConfirmExportToWA(c, id, hex)
		case CB_BYE:
			return c.Send("Bye. /start")
		}
	}

	// bare sticker, ask user's choice.
	if c.Message().Sticker != nil {
		if matchUserS(c.Sender().ID, c.Message().Sticker.SetName) {
			return sendAskSChoice(c, c.Message().Sticker.SetName)
		} else {
			return sendAskSDownloadChoice(c, c.Message().Sticker)
		}
	}

	//Animation is MP4 video with no sound.
	if c.Message().Animation != nil {
		return downloadGifToZip(c)
	}

	if c.Message().Photo != nil || c.Message().Document != nil {
		return sendUseCommandToImport(c)
	}

	// bare text message, expect a link, if no link, search keyword.
	link, tp := findLinkWithType(c.Message().Text)

	switch tp {
	case LINK_TG:
		if matchUserS(c.Sender().ID, path.Base(link)) {
			return sendAskTGLinkChoice(c)
		} else {
			return sendAskWantSDown(c)
		}
	case LINK_IMPORT:
		ld := &msbimport.LineData{}
		warn, err := msbimport.ParseImportLink(link, ld)
		if err != nil {
			return sendBadImportLinkWarn(c)
		}
		if warn != "" {
			switch warn {
			case msbimport.WARN_KAKAO_PREFER_SHARE_LINK:
				sendPreferKakaoShareLinkWarning(c)
			}
		}
		sendNotifySExist(c, ld.Id)
		return sendAskWantImportOrDownload(c, ld.IsEmoji)

	default:
		if c.Message().Text == "" {
			return sendNoSessionWarning(c)
		}
		// User sent plain text, attempt to search.
		if trySearchKeyword(c) {
			return sendNotifyNoSessionSearch(c)
		} else {
			return sendNoSessionWarning(c)
		}
	}
}

func confirmImport(c tele.Context, wantEmoji bool) error {
	ud := initUserData(c, "import", "waitSTitle")
	_, err := msbimport.ParseImportLink(findLink(c.Message().ReplyTo.Text), ud.lineData)
	if err != nil {
		return err
	}
	ud.stickerData.id = checkGnerateSIDFromLID(ud.lineData)
	workDir := filepath.Join(ud.workDir, ud.lineData.Id)
	sendAskTitle_Import(c)
	ud.wg.Add(1)
	prepDone := false
	defer func() {
		if !prepDone {
			ud.wg.Done()
		}
	}()
	setImportErr := func(err error) error {
		ud.mu.Lock()
		ud.importErr = err
		ud.mu.Unlock()
		return err
	}
	releaseImportSlot, err := acquireImportSlot(ud.ctx, func(status ImportQueueStatus) {
		ud.mu.Lock()
		ud.importQueue = status
		ud.mu.Unlock()
	})
	if err != nil {
		return setImportErr(err)
	}
	defer releaseImportSlot()
	err = msbimport.PrepareImportStickers(ud.ctx, ud.lineData, workDir, true, wantEmoji)
	if err != nil {
		if errors.Is(err, msbimport.ErrNoStickerFound) {
			return setImportErr(fmt.Errorf("%w: %v", errNoStickerAvailable, err))
		}
		return setImportErr(err)
	}
	if len(ud.lineData.Files) == 0 {
		return setImportErr(fmt.Errorf("%w: import completed without prepared sticker files", errNoStickerAvailable))
	}
	ud.stickerData.lAmount = ud.lineData.Amount
	ud.stickerData.isVideo = ud.lineData.IsAnimated
	if ud.lineData.IsEmoji && wantEmoji {
		ud.stickerData.stickerSetType = tele.StickerCustomEmoji
		ud.stickerData.isCustomEmoji = true
	} else {
		ud.stickerData.stickerSetType = tele.StickerRegular
	}

	//After PrepareImportStickers returns, individual LineFile might not be ready yet.
	//When transfering data to ud.stickerData.stickers, make sure to transfer finished data only.
	for _, lf := range ud.lineData.Files {
		sf := &StickerFile{
			oPath:            lf.OriginalFile,
			cPath:            lf.ConvertedFile,
			conversionStatus: lf.Status,
		}
		sf.wg.Add(1)
		ud.stickerData.stickers = append(ud.stickerData.stickers, sf)

		ud.activeWg.Add(1)
		go func(sf *StickerFile, lf *msbimport.LineFile) {
			defer ud.activeWg.Done()
			lf.Wg.Wait()
			if lf.CError != nil {
				sf.cError = lf.CError
			} else {
				sf.oPath = lf.OriginalFile
				sf.cPath = lf.ConvertedFile
			}
			sf.wg.Done()
		}(sf, lf)
	}

	prepDone = true
	ud.wg.Done()
	return nil
}

func trySearchKeyword(c tele.Context) bool {
	keywords := strings.Split(c.Text(), " ")
	if len(keywords) == 0 {
		return false
	}
	lines := searchLineS(keywords)
	if len(lines) == 0 {
		return false
	}
	sendSearchResult(20, lines, c)
	return true
}

func stateProcessing(c tele.Context) error {
	if c.Callback() != nil {
		if c.Callback().Data == "bye" {
			return cmdQuit(c)
		}
	}
	return c.Send("Processing, please wait... 作業中, 請稍後... /quit")
}

func enterAdminManage(c tele.Context) error {
	if c.Sender().ID != msbconf.AdminUid {
		return c.Send("Sorry, admin only.")
	}
	ud := initUserData(c, "manage", "waitSManage")
	ud.adminManage = true
	err := sendAdminManagedS(c)
	if err != nil {
		return sendNoSToManage(c)
	}
	return sendAskSToManage(c)
}

func waitSManage(c tele.Context) error {
	ud := udFromCtx(c)
	if ud == nil {
		return sendNoSessionWarning(c)
	}
	if c.Message().Sticker != nil {
		return prepareSManage(c, c.Message().Sticker.SetName, ud.adminManage)
	}

	link, tp := findLinkWithType(c.Message().Text)
	if tp == LINK_TG {
		return prepareSManage(c, path.Base(link), ud.adminManage)
	}
	return sendAskSToManage(c)
}

func statePrepareSManage(c tele.Context) error {
	if c.Message().ReplyTo == nil {
		return errors.New("unknown error: no reply to")
	}

	id := getSIDFromMessage(c.Message().ReplyTo)
	return prepareSManage(c, id, false)
}

func prepareSManage(c tele.Context, id string, adminManage bool) error {
	ud := udFromCtx(c)
	if ud == nil {
		ud = initUserData(c, "manage", "waitCbEditChoice")
	} else {
		ud.command = "manage"
		ud.state = "waitCbEditChoice"
	}
	ud.stickerData.id = id

	ud.lastContext = c
	if adminManage && c.Sender().ID == msbconf.AdminUid {
		ud.stickerData.ownerUID = queryStickerSetOwner(ud.stickerData.id)
		goto NEXT
	}
	if !matchUserS(c.Sender().ID, ud.stickerData.id) {
		return c.Send("Sorry, this sticker set cannot be edited. try another or /quit")
	}

NEXT:
	err := retrieveSSDetails(c, ud.stickerData.id, ud.stickerData)
	if err != nil {
		return c.Send("bad sticker set! try again or /quit")
	}
	if ud.stickerData.cAmount == 120 {
		sendStickerSetFullWarning(c)
	}
	setState(c, "waitCbEditChoice")
	return sendAskEditChoice(c)
}

func waitCbEditChoice(c tele.Context) error {
	if c.Callback() == nil {
		return sendNoCbWarn(c)
	}

	switch c.Callback().Data {
	case CB_ADD_STICKER:
		setState(c, "waitSFile")
		return sendAskStickerFile(c)
	case CB_DELETE_STICKER:
		setState(c, "waitSDel")
		return sendAskSDel(c)
	case CB_DELETE_STICKER_SET:
		setState(c, "waitCbDelset")
		return sendConfirmDelset(c)
	case CB_CHANGE_TITLE:
		setState(c, "waitSTitle")
		return sendAskTitle(c)
	case CB_BYE:
		endManageSession(c)
		terminateSession(c)
	default:
		return sendInStateWarning(c)
	}
	return nil
}

func waitSDel(c tele.Context) error {
	ud := udFromCtx(c)
	if c.Message().Sticker == nil {
		return c.Send("send sticker! try again or /quit")
	}
	if c.Message().Sticker.SetName != ud.stickerData.id {
		return c.Send("wrong sticker! try again or /quit")
	}

	err := c.Bot().DeleteSticker(c.Message().Sticker.FileID)
	if err != nil {
		c.Send("error deleting sticker! try another one or /quit")
		return err
	}
	c.Send("Delete OK. 成功刪除一張貼圖。")
	ud.stickerData.cAmount--
	if ud.stickerData.cAmount == 0 {
		deleteUserS(ud.stickerData.id)
		deleteLineS(ud.stickerData.id)
		terminateSession(c)
		return nil
	} else {
		setState(c, "waitCbEditChoice")
		return sendAskEditChoice(c)
	}
}

func waitCbDelset(c tele.Context) error {
	if c.Callback() == nil {
		setState(c, "waitCbEditChoice")
		return sendAskEditChoice(c)
	}
	if c.Callback().Data != CB_YES {
		setState(c, "waitCbEditChoice")
		return sendAskEditChoice(c)
	}
	ud := udFromCtx(c)
	setState(c, "process")
	c.Send("please wait...")

	ss, _ := c.Bot().StickerSet(ud.stickerData.id)
	for _, s := range ss.Stickers {
		c.Bot().DeleteSticker(s.FileID)
	}
	deleteUserS(ud.stickerData.id)
	deleteLineS(ud.stickerData.id)
	c.Send("Delete set OK. bye")
	endManageSession(c)
	terminateSession(c)
	return nil
}

func waitSType(c tele.Context) error {
	if c.Callback() == nil {
		return c.Send("Please press a button. /quit")
	}

	ud := udFromCtx(c)
	if strings.Contains(c.Callback().Data, CB_CUSTOM_EMOJI) {
		ud.stickerData.stickerSetType = tele.StickerCustomEmoji
		ud.stickerData.isCustomEmoji = true
	} else {
		ud.stickerData.stickerSetType = tele.StickerRegular
		ud.stickerData.isCustomEmoji = false
	}

	sendAskTitle(c)
	setState(c, "waitSTitle")
	return nil
}

func waitSFile(c tele.Context) error {
	if c.Callback() != nil {
		switch c.Callback().Data {
		case CB_DONE_ADDING:
			goto NEXT
		case CB_BYE:
			terminateSession(c)
			return nil
		default:
			return sendPromptStopAdding(c)
		}
	}
	if c.Message().Media() != nil {
		err := appendMedia(c)
		if err != nil {
			c.Reply("Failed processing this file. 處理此檔案時錯誤:\n" + sanitizeErrorText(err))
		}
		return nil
	} else {
		return sendPromptStopAdding(c)
	}
NEXT:
	ud := udFromCtx(c)
	if ud == nil || ud.stickerData == nil {
		return nil
	}
	if len(ud.stickerData.stickers) == 0 {
		return c.Send("No image received. try again or /quit")
	}

	setState(c, "waitEmojiChoice")
	sendAskEmoji(c)

	return nil
}

func waitSTitle(c tele.Context) error {
	ud := udFromCtx(c)
	command := ud.command

	// User sent text instead of clicking button.
	if c.Callback() == nil {
		if command == "create" || command == "import" {
			ud.stickerData.title = c.Message().Text
		} else if command == "manage" {
			err := c.Bot().SetStickerSetTitle(c.Recipient(), c.Message().Text, ud.stickerData.id)
			setState(c, "waitCbEditChoice")
			if err != nil {
				log.Warnln(err)
				return sendSSTitleFailedToChanged(c)
			} else {
				return sendSSTitleChanged(c)
			}
		} else {
			return nil
		}
		// User clicked a button, only command "import" is allowed.
	} else {
		//Reject.
		if command != "import" {
			return nil
		}
		titleIndex, atoiErr := strconv.Atoi(c.Callback().Data)
		if atoiErr == nil && titleIndex != -1 {
			ud.stickerData.title = ud.lineData.I18nTitles[titleIndex] + " @" + botName
		} else {
			ud.stickerData.title = ud.lineData.Title + " @" + botName
		}
	}

	if !checkTitle(ud.stickerData.title) {
		return c.Send("bad title! try again or /quit")
	}

	switch command {
	case "import":
		setState(c, "waitEmojiChoice")
		return sendAskEmoji(c)
	case "create":
		setState(c, "waitSID")
		sendAskID(c)
	}

	return nil
}

func waitSID(c tele.Context) error {
	var id string
	if c.Callback() != nil {
		if c.Callback().Data == "auto" {
			udFromCtx(c).stickerData.id = "sticker_" + secHex(4) + "_by_" + botName
			goto NEXT
		}
	}

	id = regexAlphanum.FindString(c.Message().Text)
	if !checkID(id) {
		return sendBadIDWarn(c)
	}
	id = id + "_by_" + botName
	if _, err := c.Bot().StickerSet(id); err == nil {
		return sendIDOccupiedWarn(c)
	}
	udFromCtx(c).stickerData.id = id

NEXT:
	setState(c, "waitSFile")
	return sendAskStickerFile(c)
}

func waitEmojiChoice(c tele.Context) error {
	ud := udFromCtx(c)
	if ud == nil || ud.stickerData == nil {
		return nil
	}
	if c.Callback() != nil {
		switch c.Callback().Data {
		case "random":
			ud.stickerData.emojis = []string{"⭐"}
		case "manual":
			if !ud.beginSessionWork() {
				if err := sessionContextErr(ud); err != nil {
					return err
				}
				return nil
			}
			defer ud.endSessionWork()

			pText, teleMsg, _ := sendProcessStarted(ud, c, "preparing...")
			setState(c, ST_PROCESSING)
			if err := waitImportPreparation(ud, newProgressEditor(pText, teleMsg, c)); err != nil {
				return err
			}
			ud.commitChans = make([]chan bool, len(ud.stickerData.stickers))
			for i := range ud.stickerData.stickers {
				ud.commitChans[i] = make(chan bool)
			}
			setState(c, "waitSEmojiAssign")
			return sendAskEmojiAssign(c)
		default:
			return nil
		}
	} else {
		emojis := findEmojis(c.Message().Text)
		if emojis == "" {
			return c.Reply("Send emoji or press button a button.\n請傳送emoji或點選按鈕。 /quit")
		}
		ud.stickerData.emojis = []string{emojis}
	}

	setState(c, ST_PROCESSING)

	if !ud.beginSessionWork() {
		if ud.ctx != nil && ud.ctx.Err() != nil {
			return ud.ctx.Err()
		}
		return nil
	}
	sessionWorkDone := false
	defer func() {
		if !sessionWorkDone {
			ud.endSessionWork()
		}
	}()
	err := submitStickerSetAuto(!(ud.command == "manage"), c)
	ud.endSessionWork()
	sessionWorkDone = true
	if err != nil {
		return err
	}
	endSession(c)
	return nil
}

func waitImportPreparation(ud *UserData, pe *progressEditor) error {
	done := make(chan struct{})
	go func() {
		ud.wg.Wait()
		close(done)
	}()

	// Poll fast so the download byte counter updates smoothly; the editor
	// coalesces and de-duplicates the edits.
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	editWaiting := func() {
		pe.set(importPreparationProgressText(ud))
	}
	editWaiting()

	for {
		select {
		case <-done:
			ud.mu.Lock()
			importErr := ud.importErr
			ud.mu.Unlock()
			if importErr != nil {
				return importErr
			}
			if len(ud.stickerData.stickers) == 0 {
				return errNoStickerAvailable
			}
			return nil
		case <-ticker.C:
			editWaiting()
		case <-ud.ctx.Done():
			return ud.ctx.Err()
		}
	}
}

func importPreparationProgressText(ud *UserData) string {
	ud.mu.Lock()
	status := ud.importQueue
	ud.mu.Unlock()

	// Only show queue position while actually waiting behind other imports.
	// "active N of N" here is internal slot-utilization, not progress — it reads
	// like a stuck progress bar to users, so the active case shows no counter.
	if status.Position > 0 {
		return fmt.Sprintf("<code>Waiting in import queue / 匯入排隊中...\n       position %d of %d</code>", status.Position, status.Waiting)
	}
	// Show the concrete sub-phase so it's clearly not stuck: byte progress while
	// the zip downloads, then extracting, then per-file processing for animated
	// packs (APNG fixup is the slow part). Falls back to a generic message.
	if ld := ud.lineData; ld != nil {
		total := ld.DLBytesTotal.Load()
		downloaded := ld.DLBytesDone.Load()
		if total > 0 && downloaded < total {
			return fmt.Sprintf("<code>Downloading pack / 下載貼圖包中...\n       %.1f / %.1f MB</code>", float64(downloaded)/1e6, float64(total)/1e6)
		}
		switch ld.PrepStage.Load() {
		case msbimport.PREP_STAGE_EXTRACTING:
			return "<code>Extracting / 解壓縮中...</code>"
		case msbimport.PREP_STAGE_DOWNLOADING:
			if filesTotal := ld.PrepFilesTotal.Load(); filesTotal > 0 {
				return fmt.Sprintf("<code>Downloading stickers / 下載貼圖中...\n       %d of %d</code>", ld.PrepFilesDone.Load(), filesTotal)
			}
			return "<code>Downloading stickers / 下載貼圖中...</code>"
		case msbimport.PREP_STAGE_PROCESSING:
			if filesTotal := ld.PrepFilesTotal.Load(); filesTotal > 0 {
				return fmt.Sprintf("<code>Processing files / 處理檔案中...\n       %d of %d</code>", ld.PrepFilesDone.Load(), filesTotal)
			}
			return "<code>Processing files / 處理檔案中...</code>"
		}
	}
	return "<code>Preparing import / 準備匯入中...</code>"
}

func waitSEmojiAssign(c tele.Context) error {
	emojiList := findEmojiList(c.Message().Text)
	if len(emojiList) == 0 {
		return c.Reply("Please send emoji and keywords(optional).\n請傳送emoji和 關鍵字(可選)。\ntry again or /quit")
	}
	keywords := stripEmoji(c.Message().Text)
	keywordList := []string{}
	if len(keywords) > 0 {
		keywordList = strings.Split(keywords, " ")
	}

	ud := udFromCtx(c)
	if ud == nil || ud.stickerData == nil {
		return nil
	}
	setState(c, ST_PROCESSING)

	err := submitStickerManual(!(ud.command == "manage"), ud.stickerData.pos, emojiList, keywordList, c)
	if err != nil {
		return err
	}
	ud.stickerData.pos += 1
	if ud.stickerData.pos == ud.stickerData.lAmount {
		return sendProcessingStickers(c)
	} else {
		setState(c, "waitSEmojiAssign")
		return sendAskEmojiAssign(c)
	}
}

func waitSearchKeyword(c tele.Context) error {
	keywords := strings.Split(c.Text(), " ")
	lines := searchLineS(keywords)
	if len(lines) == 0 {
		return sendSearchNoResult(c)
	}
	sendSearchResult(-1, lines, c)
	terminateSession(c)
	return nil
}
