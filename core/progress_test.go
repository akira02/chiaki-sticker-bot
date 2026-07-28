package core

import (
	"sync"
	"testing"
	"time"
)

func newTestProgressEditor() (*progressEditor, func() []string) {
	var mu sync.Mutex
	var sent []string
	pe := &progressEditor{
		edit: func(text string) error {
			mu.Lock()
			defer mu.Unlock()
			sent = append(sent, text)
			return nil
		},
	}
	return pe, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string{}, sent...)
	}
}

func TestProgressEditorCoalescesInsteadOfDropping(t *testing.T) {
	pe, sent := newTestProgressEditor()

	pe.set("1 of 32")
	// Inside the throttle window: these must not be sent, but the newest one must
	// not be lost either.
	pe.set("2 of 32")
	pe.set("3 of 32")

	if got := sent(); len(got) != 1 || got[0] != "1 of 32" {
		t.Fatalf("expected only the first update to be sent, got %v", got)
	}

	// Window closed: the next update carries the newest text, and the skipped
	// intermediate values are simply superseded rather than leaving a stale count.
	pe.mu.Lock()
	pe.lastSent = time.Now().Add(-progressEditInterval - time.Second)
	pe.mu.Unlock()
	pe.set("4 of 32")

	got := sent()
	if len(got) != 2 || got[1] != "4 of 32" {
		t.Fatalf("expected the newest text to be sent once the window opened, got %v", got)
	}
}

func TestProgressEditorSkipsIdenticalText(t *testing.T) {
	pe, sent := newTestProgressEditor()

	pe.set("same")
	pe.setNow("same")

	if got := sent(); len(got) != 1 {
		t.Fatalf("identical text should not be re-sent, got %v", got)
	}
}

func TestProgressEditorSetNowBypassesThrottle(t *testing.T) {
	pe, sent := newTestProgressEditor()

	pe.set("31 of 32")
	pe.setNow("32 of 32")

	got := sent()
	if len(got) != 2 || got[1] != "32 of 32" {
		t.Fatalf("terminal update must bypass the throttle, got %v", got)
	}
}

func TestProgressEditorRetriesAfterFailedEdit(t *testing.T) {
	var mu sync.Mutex
	var sent []string
	fail := true
	pe := &progressEditor{
		edit: func(text string) error {
			mu.Lock()
			defer mu.Unlock()
			sent = append(sent, text)
			if fail {
				return errTestEditFailed
			}
			return nil
		},
	}

	pe.setNow("1 of 32")
	mu.Lock()
	fail = false
	mu.Unlock()
	// A failed edit left the old text on screen, so the same text must be sendable.
	pe.setNow("1 of 32")

	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 2 {
		t.Fatalf("expected the failed edit to be retried, got %v", sent)
	}
}

var errTestEditFailed = errTestEdit{}

type errTestEdit struct{}

func (errTestEdit) Error() string { return "edit failed" }
