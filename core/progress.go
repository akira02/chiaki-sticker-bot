package core

import (
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	tele "gopkg.in/telebot.v3"
)

// Telegram rate-limits edits of the same message, so progress updates must be
// throttled. The throttle coalesces instead of dropping: the newest text is kept
// and sent once the window closes, so the counter can never sit on a stale value
// just because its update landed inside the window.
const progressEditInterval = 3 * time.Second

type progressEditor struct {
	// edit delivers one progress text to Telegram. Injectable so the throttling
	// logic is testable without a bot.
	edit func(text string) error

	mu         sync.Mutex
	lastText   string
	lastSent   time.Time
	pending    string
	hasPending bool

	// sendMu keeps concurrent flushes (heartbeat goroutine vs main flow) from
	// racing each other into out-of-order edits.
	sendMu sync.Mutex
}

func newProgressEditor(originalText string, teleMsg *tele.Message, c tele.Context) *progressEditor {
	return &progressEditor{
		edit: func(text string) error {
			return editProgressMsg(0, 0, text, originalText, teleMsg, c)
		},
	}
}

// set records the newest progress text and sends it if the throttle window is
// open; otherwise it stays pending until the next set or flush.
func (p *progressEditor) set(text string) {
	p.record(text)
	p.send(false)
}

// setNow bypasses the throttle. For terminal states (last sticker, success),
// where losing the update would leave a permanently wrong message on screen.
func (p *progressEditor) setNow(text string) {
	p.record(text)
	p.send(true)
}

func (p *progressEditor) record(text string) {
	p.mu.Lock()
	p.pending = text
	p.hasPending = true
	p.mu.Unlock()
}

func (p *progressEditor) send(force bool) {
	p.mu.Lock()
	if !p.hasPending {
		p.mu.Unlock()
		return
	}
	// Identical text is rejected by Telegram anyway; drop it instead of burning
	// a call from the rate budget.
	if p.pending == p.lastText {
		p.hasPending = false
		p.mu.Unlock()
		return
	}
	if !force && time.Since(p.lastSent) < progressEditInterval {
		p.mu.Unlock()
		return
	}
	text := p.pending
	p.hasPending = false
	p.lastText = text
	p.lastSent = time.Now()
	p.mu.Unlock()

	p.sendMu.Lock()
	err := p.edit(text)
	p.sendMu.Unlock()
	if err == nil {
		return
	}
	if strings.Contains(err.Error(), "message is not modified") {
		return
	}
	// The edit never landed, so Telegram still shows the previous text; forget it
	// so an identical follow-up isn't deduped away.
	log.Warnln("progressEditor: edit failed:", err)
	p.mu.Lock()
	if p.lastText == text {
		p.lastText = ""
	}
	p.mu.Unlock()
}
