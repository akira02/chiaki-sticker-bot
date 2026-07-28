package core

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/star-39/moe-sticker-bot/pkg/msbimport"
	tele "gopkg.in/telebot.v3"
)

func sendStartMessage(c tele.Context) error {
	message := `
Hi! I'm <b>Chiaki Sticker Bot</b>! Please:
• Use <b>/import</b> or send <b>LINE/Kakao sticker share link</b> to import or download.
• Send <b>Telegram sticker/link/GIF</b> to download.
• Send <b>keywords</b> to search sticker sets.
• Use <b>/create</b> or <b>/manage</b> to create or manage sticker sets and CustomEmoji.
• Check all available commands: <b>/command_list</b>.

你好！歡迎使用 <b>Chiaki Sticker Bot</b>！請：
• 使用 <b>/import</b> 或傳送 <b>LINE/Kakao 貼圖包分享連結</b>來匯入或下載。
• 傳送 <b>Telegram 貼圖／連結／GIF</b> 來下載。
• 傳送 <b>/search</b> 來搜尋貼圖包。
• 使用 <b>/create</b> 或 <b>/manage</b> 來新增或管理貼圖包和表情貼。
• 傳送 <b>/command_list</b> 檢視所有可用指令。
`
	return c.Send(message, tele.ModeHTML, tele.NoPreview)
}

func sendCommandList(c tele.Context) error {
	message := `
<b>/import</b>  <b>/search</b> LINE/Kakao stickers.<code>
匯入或搜尋LINE/Kaka貼圖包.</code>
<b>/download</b>  <b>/create</b>  <b>/manage</b> Telegram stickers.<code>
下載、新增、管理Telegram貼圖包.</code>
<b>/faq  /about  /changelog  /privacy</b><code>
常見問題/關於/更新紀錄/隱私</code>
`

	return c.Send(message, tele.ModeHTML, tele.NoPreview)
}

func sendAboutMessage(c tele.Context) {
	c.Send(fmt.Sprintf(`
<b>Chiaki Sticker Bot</b>
A Telegram sticker bot — import LINE/Kakao stickers, create and manage your own sticker sets, and download stickers with ease.

Telegram 貼圖機器人，支援匯入 LINE/Kakao 貼圖、新增與管理貼圖包，以及下載貼圖。

<a href="https://github.com/akira02/chiaki-sticker-bot">GitHub: akira02/chiaki-sticker-bot</a>
Forked from the great work of <a href="https://github.com/star-39/moe-sticker-bot">star-39/moe-sticker-bot</a>.
<code>
This free(as in freedom) software is released under the GPLv3 License.
Comes with ABSOLUTELY NO WARRANTY! All rights reserved.
本 BOT 為免費提供的自由軟體，您可以自由使用/分發，惟無任何保用(warranty)！
本軟體授權於通用公眾授權條款(GPL)v3，保留所有權利。
</code>
Version／版本: %s
`, BOT_VERSION), tele.ModeHTML)
}

func sendFAQ(c tele.Context) {
	c.Send(fmt.Sprintf(`
<b>Please hit Star for this project on Github if you like this bot!
如果您喜歡這個bot, 歡迎在Github給本專案標Star喔!
https://github.com/akira02/chiaki-sticker-bot</b>
------------------------------------
<b>Q: I got stucked! I can't quit from command!
我卡住了! 我沒辦法從指令中退出!</b>
A: Please send /quit to interrupt.
請傳送 /quit 來中斷。

<b>Q: Why ID has suffix: _by_%s ?
為甚麼ID的末尾有: _by_%s ?</b>
A: It's forced by Telegram, bot created sticker set must have its name in ID suffix.
因為這個是Telegram的強制要求, 由bot創造的貼圖ID末尾必須有bot名字。

<b>Q: Who owns the sticker sets the bot created?
    BOT創造的貼圖包由誰所有?</b>
A: It's you of course. You can manage them through /manage or Telegram's official @Stickers bot.
    當然是您。您可以通過 /manage 指令或者Telegram官方的 @Stickers 管理您的貼圖包。
`, botName, botName), tele.ModeHTML)
}

func sendChangelog(c tele.Context) error {
	return c.Send(`
Details: 詳細:
https://github.com/akira02/chiaki-sticker-bot#changelog
v2.5.0-RC1(20240528)
* Support mix-typed sticker set.
* You can add video to static set and vice versa.
* Removed WhatsApp export temporarily .
* 支援混合貼圖包。
* 貼圖包可以同時存入靜態與動態貼圖。
* 暫時移除WhatsApp匯出功能。

v2.4.0-RC1-RC4(20240304)
* Support Importing LINE Emoji into CustomEmoji.
* Support creating CustomEmoji.
* Support editing sticker emoji and title.
* 支援LINE表情貼匯入。
* 支援新增表情貼。
* 支援修改貼圖Emoji/貼圖包標題。

v2.3.13-v2.3.15(20230228)
* Support region locked LINE Message sticker.
* Support TGS(Animated) sticker export.
* Fix TGS(Animated) sticker download. 
* 支援有區域鎖的line訊息貼圖。
* 支援TGS貼圖匯出。
* 修復TGS(動態)貼圖下載問題.

v2.3.10(20230217)
  * Fix kakao import fatal, support more animated kakao.
  * 修復KAKAO匯入錯誤, 支援更多KAKAO動態貼圖.

v2.3.x (20230216)
  * Fix flood limit error during import.
  * Fix animated kakao treated as static.
  * Improved static kakao quality.
  * Support changing sticker title.
  * 修復匯入貼圖時flood limit錯誤。
  * 修復動態KAKAO被當作靜態.
  * 提升靜態KAKAO畫質.
  * 支援修改貼圖包標題.
  
v2.2.0 (20230131)
  * Support animated kakao sticker.
  * 支援動態kakao貼圖。

v2.1.0 (20230129)
  * Support exporting sticker to WhatsApp.
  * 支援匯出貼圖到WhatsApp

v2.0.0 (20230105)
  * Use new WebApp from /manage command to edit sticker set with ease.
  * Send text or use /search command to search imported LINE/kakao sticker sets by all users.
  * Auto import now happens on backgroud.
  * Downloading sticker set is now lot faster.
  * Fix many LINE import issues.
  * 通過 /manage 指令使用新的WebApp輕鬆管理貼圖包.
  * 直接傳送文字或使用 /search 指令來搜尋所有用戶匯入的LINE/KAKAO貼圖包.
  * 自動匯入現在會在背景處理.
  * 下載整個貼圖包的速度現在會快許多.
  * 修復了許多LINE貼圖匯入的問題.
	`, tele.NoPreview)
}

func sendPrivacy(c tele.Context) error {
	return c.Send(`
<b>Privacy Notice:</b>
This bot collects limited data to operate and improve the service.

<b>What we collect:</b>
• <b>Failure events</b> — if /import fails, your ID, action type, and failure reason are logged for diagnosing.
• <b>Sticker ownership</b> — if you use /import or /create successfully, your user ID is associated with the sticker set so that /manage can identify your sets.

<b>What we do NOT collect:</b>
• Message content
• Sticker files you send
• Any data from users who only browse or interact with sticker sets they did not create

All stored data is used solely to operate this bot and will never be shared with any third party.
The bot server is located in Singapore. Local laws may apply.
This bot is free and open source: https://github.com/akira02/chiaki-sticker-bot

<b>隱私聲明:</b>
本 bot 會收集有限的資料以維持服務運作。

<b>收集的資料：</b>
• <b>使用紀錄</b> — 當您使用 /import 時，您的 ID、動作類型及失敗原因會被記錄，用於診斷問題。
• <b>貼圖包歸屬</b> — 當您成功使用 /import 或 /create 時，您的用戶 ID 會與所建立的貼圖包關聯，以供 /manage 識別您的貼圖包。

<b>不收集的資料：</b>
• 訊息內容
• 您傳送的貼圖檔案
• 未使用上述指令的用戶的任何資料

所有儲存的資料僅用於維持本 bot 的運作，不會分享給任何第三方。
本 bot 伺服器位於新加坡，將適用當地法律。
本 bot 為自由開放原始碼軟體：https://github.com/akira02/chiaki-sticker-bot
`, tele.ModeHTML, tele.NoPreview)
}

func sendAskEmoji(c tele.Context) error {
	selector := &tele.ReplyMarkup{}
	btnManu := selector.Data("Assign separately/分別設定", "manual")
	btnRand := selector.Data(`Batch assign as/一併設定為 "⭐"`, "random")
	selector.Inline(selector.Row(btnManu), selector.Row(btnRand))

	return c.Send(`
Telegram requires emoji to and keywords for each sticker:
• Press "Assign separately" to assign emoji and keywords one by one.
• Send an emoji to do batch assign.
Telegram要求為每張貼圖分別設定emoji和關鍵字:
• 按下"分別設定"來為每個貼圖分別設定相應的emoji和關鍵字。
• 傳送一個emoji來為全部貼圖設定成一樣的。
`, selector)
}

func sendConfirmExportToWA(c tele.Context, sn string, hex string) error {
	selector := &tele.ReplyMarkup{}
	baseUrl, _ := url.JoinPath(msbconf.WebappUrl, "export")
	webAppUrl := fmt.Sprintf("%s?sn=%s&hex=%s", baseUrl, sn, hex)
	log.Debugln("webapp export link is:", webAppUrl)
	webapp := tele.WebApp{URL: webAppUrl}
	btnExport := selector.WebApp("Continue export/繼續匯出 →", &webapp)
	selector.Inline(selector.Row(btnExport))

	return c.Reply(`
Exporting to WhatsApp requires <a href="https://github.com/star-39/msb_app">Msb App</a> due to their restrictions, then press "Continue export".
匯出到WhatsApp需要手機上安裝<a href="https://github.com/star-39/msb_app">Msb App</a>, 然後按下"繼續匯出".

Download:下載:
<b>iPhone:</b> AppStore(N/A.暫無), <a href="https://github.com/star-39/msb_app/releases/latest/download/msb_app.ipa">IPA</a>
<b>Android:</b> GooglePlay(N/A.暫無), <a href="https://github.com/star-39/msb_app/releases/latest/download/msb_app.apk">APK</a>
`, tele.ModeHTML, tele.NoPreview, selector)
}

func genSDnMnEInline(canManage bool, isTGS bool, sn string) *tele.ReplyMarkup {
	selector := &tele.ReplyMarkup{}
	btnSingle := selector.Data("Download this sticker/下載這張貼圖", CB_DN_SINGLE)
	btnAll := selector.Data("Download sticker set/下載整個貼圖包", CB_DN_WHOLE)
	btnMan := selector.Data("Manage sticker set/管理這個貼圖包", CB_MANAGE)
	// btnExport := selector.Data("Export to WhatsApp/匯出到WhatsApp", CB_EXPORT_WA)
	if canManage {
		selector.Inline(selector.Row(btnSingle), selector.Row(btnAll), selector.Row(btnMan))
	} else {
		if isTGS {
			//If is TGS, do not support export to WA.
			selector.Inline(selector.Row(btnSingle), selector.Row(btnAll))
		} else {
			selector.Inline(selector.Row(btnSingle), selector.Row(btnAll))
		}
	}
	return selector
}

func sendAskSDownloadChoice(c tele.Context, s *tele.Sticker) error {
	selector := genSDnMnEInline(false, s.Animated, s.SetName)
	return c.Reply(`
You can download this sticker or the whole sticker set, please select below.
您可以下載這個貼圖或者其所屬的整個貼圖包, 請選擇:
`, selector)
}

func sendAskSChoice(c tele.Context, sn string) error {
	selector := genSDnMnEInline(true, false, sn)
	return c.Reply(`
You own this sticker set. You can download or manage this sticker set, please select below.
您擁有這個貼圖包. 您可以下載或者管理這個貼圖包, 請選擇:
`, selector)
}

func sendAskTGLinkChoice(c tele.Context) error {
	selector := &tele.ReplyMarkup{}
	btnManu := selector.Data("Download sticker set/下載整個貼圖包", CB_DN_WHOLE)
	btnMan := selector.Data("Manage sticker set/管理這個貼圖包", CB_MANAGE)
	selector.Inline(selector.Row(btnManu), selector.Row(btnMan))
	return c.Reply(`
You own this sticker set. You can download or manage this sticker set, please select below.
您擁有這個貼圖包. 您可以下載或者管理這個貼圖包, 請選擇:
`, selector)
}

func sendAskWantSDown(c tele.Context) error {
	selector := &tele.ReplyMarkup{}
	btn1 := selector.Data("Yes", CB_DN_WHOLE)
	btnNo := selector.Data("No", CB_BYE)
	selector.Inline(selector.Row(btn1), selector.Row(btnNo))
	return c.Reply(`
You can download this sticker set. Press Yes to continue.
您可以下載這個貼圖包, 按下Yes來繼續.
`, selector)
}

func sendAskWantImportOrDownload(c tele.Context, avalAsEmoji bool) error {
	msg := ""
	selector := &tele.ReplyMarkup{}
	btnImportSticker := selector.Data("Import as sticker set/作為 Telegram 普通貼圖包匯入", CB_OK_IMPORT)
	btnImportEmoji := selector.Data("Import as CustomEmoji/作為表情貼匯入", CB_OK_IMPORT_EMOJI)
	btnDownload := selector.Data("Download/下載", CB_OK_DN)
	if avalAsEmoji {
		selector.Inline(selector.Row(btnImportSticker), selector.Row(btnImportEmoji), selector.Row(btnDownload))
		msg = `
You can import this sticker set to Telegram or download it.
Import as Custom Emoji is also available, however you will need Telegram Premium to send them.
您可以下載或匯入這個貼圖包到Telegram.
也可以作為表情貼匯入，但是傳送需要Telegram會員。`
	} else {
		selector.Inline(selector.Row(btnImportSticker), selector.Row(btnDownload))
		msg = `
You can import this sticker set to Telegram or download it.
您可以下載或匯入這個貼圖包到Telegram.`
	}

	return c.Reply(msg, selector)
}

func sendAskWhatToDownload(c tele.Context) error {
	return c.Send("Please send a sticker that you want to download, or its share link(can be either Telegram or LINE ones)\n" +
		"請傳送想要下載的貼圖, 或者是貼圖包的分享連結(可以是Telegram或LINE連結).")
}

func sendAskTitle_Import(c tele.Context) error {
	ld := users.data[c.Sender().ID].lineData
	ld.TitleWg.Wait()
	log.Debug("titles are::")
	log.Debugln(ld.I18nTitles)
	selector := &tele.ReplyMarkup{}

	var titleButtons []tele.Row
	var titleText string
	for i, t := range ld.I18nTitles {
		if t == "" {
			continue
		}
		title := escapeTagMark(t) + " @" + botName
		btn := selector.Data(title, strconv.Itoa(i))
		row := selector.Row(btn)
		titleButtons = append(titleButtons, row)
		titleText = titleText + "\n<code>" + title + "</code>"
	}

	if len(titleButtons) == 0 {
		btnDefault := selector.Data(escapeTagMark(ld.Title)+" @"+botName, CB_DEFAULT_TITLE)
		titleButtons = []tele.Row{selector.Row(btnDefault)}
	}
	selector.Inline(titleButtons...)

	return c.Send("Please send a title for this sticker set. You can also select an original title below:\n"+
		"請傳送貼圖包的標題.您也可以按下面的按鈕自動填上合適的原版標題:\n"+
		titleText, selector, tele.ModeHTML)
}

func sendAskTitle(c tele.Context) error {
	return c.Send("Please send a title for this sticker set.\n" +
		"請傳送貼圖包的標題.")
}

func sendAskID(c tele.Context) error {
	selector := &tele.ReplyMarkup{}
	btnAuto := selector.Data("Auto Generate/自動生成", "auto")
	selector.Inline(selector.Row(btnAuto))
	return c.Send(`
Please send an ID for sticker set, used in share link.
Can contain alphanum and underscore only.
請設定貼圖包的ID, 用於分享連結。
只可以含有英文,數字,下劃線。
For example: 例如:
<code>My_favSticker21</code>

ID is usually not important, you can press Auto Generate.
ID通常不重要, 您可以按下"自動生成".`, selector, tele.ModeHTML)
}

func sendAskImportLink(c tele.Context) error {
	return c.Send(`
Please send LINE/kakao store link of the sticker set. You can obtain this link from App by going to sticker store and tapping Share->Copy Link.
請傳送貼圖包的LINE/kakao Store連結. 您可以在App裡的貼圖商店按右上角的分享->複製連結來取得連結.
For example: 例如:
<code>https://store.line.me/stickershop/product/7673/ja</code>
<code>https://e.kakao.com/t/pretty-all-friends</code>
<code>https://emoticon.kakao.com/items/lV6K2fWmU7CpXlHcP9-ysQJx9rg=?referer=share_link</code>
`, tele.ModeHTML)
}

func sendNotifySExist(c tele.Context, lineID string) bool {
	lines := queryLineS(lineID)
	if len(lines) == 0 {
		return false
	}
	message := "This sticker set exists in our database, you can continue import or just use them if you want.\n" +
		"此套貼圖包已經存在於資料庫中, 您可以繼續匯入, 或者使用下列現成的貼圖包\n\n"

	var entries []string
	for _, l := range lines {
		if l.Ae {
			entries = append(entries, fmt.Sprintf(`<a href="%s">%s</a>`, "https://t.me/addstickers/"+l.Tg_id, l.Tg_title))
		} else {
			// append to top
			entries = append([]string{fmt.Sprintf(`★ <a href="%s">%s</a>`, "https://t.me/addstickers/"+l.Tg_id, l.Tg_title)}, entries...)
		}
	}
	if len(entries) > 5 {
		entries = entries[:5]
	}
	message += strings.Join(entries, "\n")
	c.Send(message, tele.ModeHTML)
	return true
}

func sendSearchResult(entriesWant int, lines []LineStickerQ, c tele.Context) error {
	var entries []string
	message := "Search Results: 搜尋結果：\n"

	for _, l := range lines {
		l.Tg_title = strings.TrimSuffix(l.Tg_title, " @"+botName)
		if l.Ae {
			entries = append(entries, fmt.Sprintf(`<a href="%s">%s</a>`, "https://t.me/addstickers/"+l.Tg_id, l.Tg_title))
		} else {
			// append to top
			entries = append([]string{fmt.Sprintf(`★ <a href="%s">%s</a>`, "https://t.me/addstickers/"+l.Tg_id, l.Tg_title)}, entries...)
		}
	}

	if entriesWant == -1 && len(entries) > 120 {
		c.Send("Too many results, please narrow your keyword, truncated to 120 entries.\n" +
			"搜尋結果過多，已縮減到120個，請使用更準確的搜尋關鍵字。")
		entries = entries[:120]
	}
	if entriesWant != -1 && len(entries) > entriesWant {
		entries = entries[:entriesWant]
	}
	if len(entries) > 30 {
		eChunks := chunkSlice(entries, 30)
		for _, eChunk := range eChunks {
			msgToSend := message + strings.Join(eChunk, "\n")
			c.Send(msgToSend, tele.ModeHTML)
		}
	} else {
		message += strings.Join(entries, "\n")
		c.Send(message, tele.ModeHTML)
	}

	return nil
}

func sendAskStickerFile(c tele.Context) error {
	return c.Send("Please send images/photos/stickers(less than 120 in total),\n" +
		"or send an archive containing image files,\n" +
		"wait until upload complete, then tap 'Done adding'.\n\n" +
		"請傳送任意格式的圖片/影片/貼圖(少於120張)\n" +
		"或者傳送內有貼圖檔案的壓縮檔案,\n" +
		"等候所有檔案上載完成, 然後按下「上傳完成」\n")
}

func sendInStateWarning(c tele.Context) error {
	command := users.data[c.Sender().ID].command
	state := users.data[c.Sender().ID].state

	return c.Send(fmt.Sprintf(`
Please send content according to instructions.
請按照bot提示傳送相應內容.
Current command: %s
Current state: %s
You can also send /quit to terminate session.
您也可以傳送 /quit 來中斷對話.
`, command, state))
}

func sendNoSessionWarning(c tele.Context) error {
	return c.Send("Please use /start or send LINE/kakao/Telegram links or stickers.\n請使用 /start 或者傳送LINE/kakao/Telegram連結或貼圖.")
}

func sendAskSTypeToCreate(c tele.Context) error {
	selector := &tele.ReplyMarkup{}
	btnRegular := selector.Data("Regular sticker set/普通貼圖包", CB_REGULAR_STICKER)
	btnCustomEmoji := selector.Data("Custom Emoji/表情貼", CB_CUSTOM_EMOJI)

	selector.Inline(selector.Row(btnRegular), selector.Row(btnCustomEmoji))
	return c.Send("What kind of sticker set you want to create?\nNote that custom emoji can only be sent by Telegram Premium member."+
		"您想要新增何種類型的貼圖包?\n請注意只有Telgram會員可以傳送表情貼。", selector)
}

func sendAskEmojiAssign(c tele.Context) error {
	ud := udFromCtx(c)
	if ud == nil || ud.stickerData == nil {
		return nil
	}
	sd := ud.stickerData
	if sd.pos < 0 || sd.pos >= len(sd.stickers) {
		log.Errorf("No sticker available for emoji assignment at pos %d, total %d", sd.pos, len(sd.stickers))
		return errNoStickerAvailable
	}
	sf := sd.stickers[sd.pos]
	if sf == nil {
		log.Errorf("Sticker data is nil for emoji assignment at pos %d", sd.pos)
		return errNoStickerAvailable
	}
	total := sd.lAmount
	if total == 0 {
		total = len(sd.stickers)
	}
	waitedForPreview := false
	if sf.fileID == "" && sf.oPath == "" {
		sf.wg.Wait()
		waitedForPreview = true
	}
	if err := sessionContextErr(ud); err != nil {
		return err
	}
	if waitedForPreview && sf.cError != nil {
		return sf.cError
	}
	if sf.fileID == "" && sf.oPath == "" {
		return errors.New("sticker preview file is not ready")
	}
	caption := fmt.Sprintf(`
Send emoji(s) representing this sticker.
請傳送代表這個貼圖的emoji(可以多個).

%d of %d
`, sd.pos+1, total)

	if sf.fileID != "" {
		msg, _ := c.Bot().Send(c.Sender(), &tele.Sticker{
			File: tele.File{FileID: sf.fileID},
		})
		_, err := c.Bot().Reply(msg, caption)
		return err
	}

	err := c.Send(&tele.Video{
		File:    tele.FromDisk(sf.oPath),
		Caption: caption,
	})
	if err != nil {
		err2 := c.Send(&tele.Video{
			File:    tele.FromDisk(sf.oPath),
			Caption: caption,
		})
		if err2 != nil {
			err3 := c.Send(&tele.Document{
				File:     tele.FromDisk(sf.oPath),
				FileName: filepath.Base(sf.oPath),
				Caption:  caption,
			})
			if err3 != nil {
				err4 := c.Send(&tele.Sticker{File: tele.File{FileID: sf.oPath}})
				if err4 != nil {
					return err4
				}
			}
		}
	}
	return nil
}

func sendFatalError(err error, c tele.Context) {
	if c == nil {
		return
	}
	var errMsg string
	if err != nil {
		errMsg = sanitizeErrorText(err)
		errMsg = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(errMsg)
	}

	if isTelegramTemporaryServerError(err) {
		c.Send("<b>Telegram server is temporarily unavailable. Please try again later. /start\n"+
			"Telegram 伺服器暫時回應逾時或發生錯誤，請稍後再試。</b>\n\n"+
			"This is usually a temporary Telegram-side issue, not a problem with your sticker link.\n"+
			"這通常是 Telegram 端暫時性問題，不是你的貼圖連結錯誤。\n\n"+
			"<code>"+errMsg+"</code>", tele.ModeHTML, tele.NoPreview)
		return
	}

	c.Send("<b>Fatal error encounterd. Please try again. /start\n"+
		"發生錯誤，請點選 /start 重試。</b>\n\n"+
		"You can report this error to https://github.com/akira02/chiaki-sticker-bot/issues\n\n"+
		"<code>"+errMsg+"</code>", tele.ModeHTML, tele.NoPreview)
}

func sendExecEmojiAssignFinished(c tele.Context) error {
	ud := users.data[c.Sender().ID]
	msg := fmt.Sprintf(`
LINE Cat: <code>%s</code>
LINE ID: <code>%s</code>
TG ID: <code>%s</code>
TG Title: <a href="%s">%s</a>

Success. 成功完成. /start

If you like this bot, please give us a ⭐️
如果你喜歡這個 Bot，請幫我們按個 ⭐️
https://github.com/akira02/chiaki-sticker-bot
	`, ud.lineData.Category,
		ud.lineData.Id,
		ud.stickerData.id,
		"https://t.me/addstickers/"+ud.stickerData.id,
		escapeTagMark(ud.stickerData.title),
	)
	return c.Send(msg, tele.ModeHTML)
}

// Return:
// string: Text of the message.
// *tele.Message: The pointer of the message.
// error: error
func sendProcessStarted(ud *UserData, c tele.Context, optMsg string) (string, *tele.Message, error) {
	message := fmt.Sprintf(`
Preparing stickers, please wait...
正在準備貼圖, 請稍候...

LINE Cat: <code>%s</code>
LINE ID: <code>%s</code>
TG ID: <code>%s</code>
TG TYPE: <code>%s</code>
TG Title: <a href="%s">%s</a>

<b>Progress / 進展</b>
<code>%s</code>
`, ud.lineData.Category,
		ud.lineData.Id,
		ud.stickerData.id,
		ud.stickerData.stickerSetType,
		"https://t.me/addstickers/"+ud.stickerData.id,
		escapeTagMark(ud.stickerData.title),
		optMsg)
	ud.progress = message

	teleMsg, err := c.Bot().Send(c.Recipient(), message, tele.ModeHTML)
	ud.progressMsg = teleMsg
	return message, teleMsg, err
}

// if progressText is empty, a progress bar will be generated based on cur and total.
func editProgressMsg(cur int, total int, progressText string, originalText string, teleMsg *tele.Message, c tele.Context) error {
	defer func() {
		if r := recover(); r != nil {
			log.Errorln("editProgressMsg encountered panic! ignoring...", string(debug.Stack()))
		}
	}()

	header := originalText[:strings.LastIndex(originalText, "<code>")]
	prog := progressText

	if prog == "" {
		cur = cur + 1
		if cur != 1 && cur != total &&
			cur != int(float64(0.25)*float64(total)) &&
			cur != int(float64(0.5)*float64(total)) &&
			cur != int(float64(0.75)*float64(total)) {
			return nil
		}
		prog = progressBarText(cur, total)
	}

	messageText := header + prog
	_, err := c.Bot().Edit(teleMsg, messageText, tele.ModeHTML)
	return err
}

// progressBarText renders a 20-cell bar plus the exact count. cur is 1-based.
func progressBarText(cur int, total int) string {
	const cells = 20
	filled := 0
	if total > 0 {
		filled = cur * cells / total
	}
	if filled > cells {
		filled = cells
	}
	bar := strings.Repeat("=", filled)
	if filled < cells {
		bar += ">" + strings.Repeat(" ", cells-filled-1)
	}
	return fmt.Sprintf("<code>[%s]\n       %d of %d</code>", bar, cur, total)
}

func sendAskSToManage(c tele.Context) error {
	if c.Sender().ID == msbconf.AdminUid {
		selector := &tele.ReplyMarkup{}
		btnAdminManage := selector.Data("Admin manage", CB_ADMIN_MANAGE)
		selector.Inline(selector.Row(btnAdminManage))
		return c.Send("Send a sticker from the sticker set that want to edit,\n"+
			"or send its share link.\n\n"+
			"您想要修改哪個貼圖包? 請傳送那個貼圖包內任意一張貼圖,\n"+
			"或者是它的分享連結.", selector)
	}
	return c.Send("Send a sticker from the sticker set that want to edit,\n" +
		"or send its share link.\n\n" +
		"您想要修改哪個貼圖包? 請傳送那個貼圖包內任意一張貼圖,\n" +
		"或者是它的分享連結.")
}

func sendUserOwnedS(c tele.Context) error {
	return sendStickerSetList(c, c.Sender().ID, "You own following stickers:")
}

func sendAdminManagedS(c tele.Context) error {
	return sendStickerSetList(c, -1, "Admin manageable stickers:")
}

func sendStickerSetList(c tele.Context, uid int64, header string) error {
	usq := queryUserS(uid)
	if usq == nil {
		return errors.New("no sticker owned")
	}

	var entries []string

	for _, us := range usq {
		date := time.Unix(us.timestamp, 0).Format("2006-01-02 15:04")
		title := strings.TrimSuffix(us.tg_title, " @"+botName)
		//workaround for empty title.
		if title == "" || title == " " {
			title = "_"
		}
		entry := fmt.Sprintf(`<a href="https://t.me/addstickers/%s">%s</a>`, us.tg_id, title)
		entry += " | " + date
		entries = append(entries, entry)
	}

	if len(entries) > 30 {
		eChunks := chunkSlice(entries, 30)
		for _, eChunk := range eChunks {
			message := header + "\n"
			message += strings.Join(eChunk, "\n")
			c.Send(message, tele.ModeHTML)
		}
	} else {
		message := header + "\n"
		message += strings.Join(entries, "\n")
		c.Send(message, tele.ModeHTML)
	}
	return nil
}

func sendAskEditChoice(c tele.Context) error {
	ud := users.data[c.Sender().ID]
	selector := &tele.ReplyMarkup{}
	btnAdd := selector.Data("Add sticker/添加貼圖", CB_ADD_STICKER)
	btnDel := selector.Data("Delete sticker/刪除貼圖", CB_DELETE_STICKER)
	btnDelset := selector.Data("Delete sticker set/刪除貼圖包", CB_DELETE_STICKER_SET)
	btnChangeTitle := selector.Data("Change title/修改標題", CB_CHANGE_TITLE)
	btnExit := selector.Data("Exit/退出", "bye")

	if msbconf.WebappUrl != "" {
		baseUrl, _ := url.JoinPath(msbconf.WebappUrl, "edit")
		url := fmt.Sprintf("%s?ss=%s&dt=%d",
			baseUrl,
			ud.stickerData.id,
			time.Now().Unix())
		log.Debugln("WebApp URL is : ", url)
		webApp := &tele.WebApp{
			URL: url,
		}
		btnEdit := selector.WebApp("Change order or emoji/修改順序或Emoji", webApp)
		selector.Inline(
			selector.Row(btnAdd), selector.Row(btnDel), selector.Row(btnDelset), selector.Row(btnEdit), selector.Row(btnChangeTitle), selector.Row(btnExit))
	} else {
		selector.Inline(
			selector.Row(btnAdd), selector.Row(btnDel), selector.Row(btnDelset), selector.Row(btnChangeTitle), selector.Row(btnExit))
	}

	return c.Send(fmt.Sprintf(`
ID: <code>%s</code>
Title: <a href="https://t.me/addstickers/%s">%s</a>

What do you want to edit? Please select below:
您想要修改貼圖包的甚麼內容? 請選擇:`,
		users.data[c.Sender().ID].stickerData.id,
		ud.stickerData.id,
		ud.stickerData.title),
		selector, tele.ModeHTML)
}

func sendAskSDel(c tele.Context) error {
	return c.Send("Which sticker do you want to delete? Please send it.\n" +
		"您想要刪除哪一個貼圖? 請傳送那個貼圖")
}

func sendConfirmDelset(c tele.Context) error {
	selector := &tele.ReplyMarkup{}
	btnYes := selector.Data("Yes", CB_YES)
	btnNo := selector.Data("No", CB_NO)
	selector.Inline(selector.Row(btnYes), selector.Row(btnNo))

	return c.Send("You are attempting to delete the whole sticker set, please confirm.\n"+
		"您將要刪除整個貼圖包, 請確認.", selector)
}

func sendSFromSS(c tele.Context, ssid string, reply *tele.Message) error {
	ss, _ := c.Bot().StickerSet(ssid)
	if reply != nil {
		c.Bot().Reply(reply, &ss.Stickers[0])
	} else {
		c.Send(&ss.Stickers[0])
	}
	return nil
}

func sendFLWarning(c tele.Context) error {
	return c.Send(`
It might take longer to process this sticker set (2-8 minutes)... 
This warning indicates that you might triggered Telegram's flood limit, and bot is trying to re-submit.
Due to this mechanism, resulted sticker set might contains duplicate or missing sticker, please check manually after done.

此貼圖包可能需要更長時間處理(2-8分鐘)...
看到這一條警告表示Telegram可能限制了您新增貼圖包的頻度, 且bot正在自動嘗試重新製作, 因此得出的貼圖包可能會重複或缺失貼圖, 請在完成製作後再檢查一下.
`)
}

func sendTooManyFloodLimits(c tele.Context) error {
	return c.Send("Sorry, it seems that you have triggered Telegram's flood limit for too many times, it's recommended try again after a while.\n" +
		"抱歉, 您似乎觸發了Telegram的貼圖製作次數限制, 建議您過一段時間後再試一次.")
}

func sendNoCbWarn(c tele.Context) error {
	return c.Send("Please press a button! /quit\n請選擇按鈕!")
}

func sendBadIDWarn(c tele.Context) error {
	return c.Send(`
Bad ID. try again or press Auto Generate. /quit
Can contain alphanum and underscore only, must begin with alphabet, must not contain consecutive underscores.
只可以含有英文,數字,下劃線, 必須由英文字母開頭，不可以有連續下劃線.
ID錯誤, 請試多一次或按下'自動生成'按鈕. /quit`)
}

func sendIDOccupiedWarn(c tele.Context) error {
	return c.Send("ID already occupied! try another one. ID已經被占用, 請試試另一個.")
}

func sendBadImportLinkWarn(c tele.Context) error {
	return c.Send("Invalid import link, make sure its a LINE Store link or kakao store link. Try again or /quit\n"+
		"無效的連結, 請檢視是否為LINE貼圖商店的連結, 或是kakao emoticon的連結\n\n"+
		"For example: 例如:\n"+
		"<code>https://store.line.me/stickershop/product/7673/ja</code>\n"+
		"<code>https://e.kakao.com/t/pretty-all-friends</code>", tele.ModeHTML)
}

func sendNoSToManage(c tele.Context) error {
	return c.Send("Sorry, you have not created any sticker set yet. You can use /import or /create .\n" +
		"抱歉, 您還未新增過貼圖包, 您可以使用 /create 或 /import 來新增貼圖包")
}

func sendPromptStopAdding(c tele.Context) error {
	selector := &tele.ReplyMarkup{}
	btnDone := selector.Data("Done adding/上傳完成", CB_DONE_ADDING)
	selector.Inline(selector.Row(btnDone))
	return c.Send("Continue sending files or press button below to stop adding.\n"+
		"請繼續傳送檔案. 或者按下方按鈕來停止", selector)
}

func replySFileOK(c tele.Context, count int) error {
	selector := &tele.ReplyMarkup{}
	btnDone := selector.Data("Done adding/上傳完成", CB_DONE_ADDING)
	selector.Inline(selector.Row(btnDone))
	return c.Reply(
		fmt.Sprintf("File OK. Got %d stickers. Continue sending files or press button below to stop adding.\n"+
			"檔案OK. 已收到%d份貼圖. 請繼續傳送檔案. 或者按下方按鈕來完成上傳", count, count), selector)
}

func sendSEditOK(c tele.Context) error {
	return c.Send(
		"Successfully edited sticker set. /start\n" +
			"成功修改貼圖包. /start")
}

func sendStickerSetFullWarning(c tele.Context) error {
	return c.Send(
		"Warning: Your sticker set is already full. You cannot add new sticker.\n" +
			"提示：目前貼圖包已滿，無法加入新的貼圖。")
}

// func sendEditingEmoji(c tele.Context) error {
// 	return c.Send("Commiting changes...\n正在套用變更，請稍候...")
// }

func sendAskSearchKeyword(c tele.Context) error {
	return c.Send("Please send a word that you want to search\n請傳送想要搜尋的內容")
}

func sendSearchNoResult(c tele.Context) error {
	message := "Sorry, no result.\n沒有結果"
	if c.Chat().Type == tele.ChatPrivate {
		message += "\nTry again or /quit\n請試試別的關鍵字或 /quit"
	}
	return c.Send(message)
}

func sendNotifyNoSessionSearch(c tele.Context) error {
	return c.Send("Here are some search results, use /search to dig deeper or /start to see available commands.\n" +
		"這些是貼圖包搜尋結果，使用 /search 詳細搜尋或 /start 來看看可用的指令。")
}

func sendUnsupportedCommandForGroup(c tele.Context) error {
	return c.Send("This command is not supported in group chat, please chat with bot directly.\n" +
		"此指令無法於群組內使用, 請與bot直接私訊.")
}

func sendBadSearchKeyword(c tele.Context) error {
	return c.Send(fmt.Sprintf(`
Please specify keyword
請指定搜尋關鍵字.

Example: 例如:
/search@%s keyword1 keyword2 ...
/search@%s nekomimi mia
`, botName, botName))
}

func sendPreferKakaoShareLinkWarning(c tele.Context) error {
	msg := `
The link you sent is a Kakao store link.
Use a share link for improved image quality and animated sticker support,
you can obtain it from KakaoTalk app by tapping share->copy link in sticker store.

您傳送的是Kakao商店的連結.
使用分享連結才能支援動態貼圖, 靜態貼圖的畫質也更高。
您可以在KakaoTalk App內的貼圖商店點選 分享->複製連結 來取得分享連結。

eg:例如: <code>https://emoticon.kakao.com/items/lV6K2fWmU7CpXlHcP9-ysQJx9rg=?referer=share_link</code>
`
	err := c.Reply(&tele.Photo{
		File:    tele.File{FileID: FID_KAKAO_SHARE_LINK},
		Caption: msg,
	}, tele.ModeHTML)
	if err != nil {
		c.Reply(msg, tele.ModeHTML)
	}
	return nil
}

func sendUseCommandToImport(c tele.Context) error {
	return c.Send("Please use /create to create sticker set using your own photos and videos. /start\n" +
		"請使用 /create 指令來使用自己的圖片和影片和新增貼圖包 /start")
}

func sendOneStickerFailedToAdd(c tele.Context, pos int, err error) error {
	if errors.Is(err, msbimport.ErrStickerTooLarge) {
		return c.Reply(stickerCompressionFailedMessage(err), tele.NoPreview)
	}
	return c.Reply(fmt.Sprintf(`
Failed to add one sticker.
一張貼圖添加失敗
Index: %d
Error: %s
`, pos, sanitizeErrorText(err)))
}

func sendStickerCompressionFailed(c tele.Context, err error) error {
	if c == nil {
		return nil
	}
	return c.Send(stickerCompressionFailedMessage(err), tele.NoPreview)
}

func stickerCompressionFailedMessage(err error) string {
	reason := ""
	if err != nil {
		reason = sanitizeErrorText(err)
	}
	return "This sticker is too large or too complex to fit Telegram's video sticker limit, even after reducing bitrate and shortening the animation.\n" +
		"這張貼圖太大或動畫太複雜；即使降低 bitrate 並縮短秒數後，仍無法壓到 Telegram 影片貼圖限制內。\n\n" +
		"Reason: " + reason + "\n" +
		"原因：" + reason + "\n\n" +
		"If this sticker should work, please report it here:\n" +
		"如果這張貼圖理論上可以匯入，請到這裡回報：\n" +
		"https://github.com/akira02/chiaki-sticker-bot/issues"
}

func sendBadSNWarn(c tele.Context) error {
	return c.Reply("Wrong sticker or link!\n貼圖或連結錯誤!")
}

func sendSSTitleChanged(c tele.Context) error {
	msg := `
Successfully changed title.
新標題設定完成`
	return c.Reply(msg, tele.ModeHTML)
}
func sendSSTitleFailedToChanged(c tele.Context) error {
	msg := `
Failed to change title, please try again.
新標題設定失敗，請再試一次。`
	return c.Reply(msg, tele.ModeHTML)
}

// func sendInvalidEmojiWarn(c tele.Context) error {
// 	return c.Reply(`
// Sorry, this emoji is invalid, it has been defaulted to ⭐️, you can edit it after done by using /manage command.
// 抱歉，這個emoji無效，並且已默認設定為⭐️，你可以在完成製作後使用 /manage 來修改。
// 	`)
// }

func sendProcessingStickers(c tele.Context) error {
	return c.Send(`
Processing stickers, please wait a while...
正在製作貼圖，請稍等...
`)
}
