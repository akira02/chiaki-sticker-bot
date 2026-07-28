package msbimport

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestConverterQueueReportsPositionAndClearsOnGrant(t *testing.T) {
	q := &converterQueue{name: "test", capacity: 1}

	release, err := q.acquire(context.Background(), nil)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	status := NewConversionStatus()
	status.Set("too large for Telegram. Compressing...")
	acquired := make(chan func(), 1)
	go func() {
		r, err := q.acquire(context.Background(), status)
		if err != nil {
			t.Errorf("queued acquire: %v", err)
			return
		}
		acquired <- r
	}()

	waitFor(t, func() bool { return strings.Contains(status.Message(), "position 1 of 1") },
		"queue position was never reported")

	release()

	r := <-acquired
	defer r()
	// The queue notice must be cleared without eating the converter's own message.
	if got := status.Message(); got != "too large for Telegram. Compressing..." {
		t.Fatalf("expected the underlying status to reappear, got %q", got)
	}
}

func TestConverterQueueWaitIsCancellable(t *testing.T) {
	q := &converterQueue{name: "test", capacity: 1}

	release, err := q.acquire(context.Background(), nil)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	go func() {
		_, err := q.acquire(ctx, nil)
		errs <- err
	}()

	cancel()
	select {
	case err := <-errs:
		if err != context.Canceled {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled wait must not block forever")
	}
}

func TestConverterQueueWaitTimesOut(t *testing.T) {
	t.Setenv("MSB_CONVERT_QUEUE_TIMEOUT_SECONDS", "1")
	q := &converterQueue{name: "test", capacity: 1}

	release, err := q.acquire(context.Background(), nil)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer release()

	started := time.Now()
	if _, err := q.acquire(context.Background(), nil); err == nil {
		t.Fatal("expected a timeout error")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("timeout took too long: %s", elapsed)
	}
}

func TestConverterQueueGrantsInFIFOOrder(t *testing.T) {
	q := &converterQueue{name: "test", capacity: 1}

	release, err := q.acquire(context.Background(), nil)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	order := make(chan int, 3)
	releases := make(chan func(), 3)
	for i := 1; i <= 3; i++ {
		index := i
		go func() {
			r, err := q.acquire(context.Background(), nil)
			if err != nil {
				t.Errorf("acquire %d: %v", index, err)
				return
			}
			order <- index
			releases <- r
		}()
		// Serialize enqueueing so the expected FIFO order is deterministic.
		waitFor(t, func() bool {
			_, _, queued := q.stats()
			return queued == index
		}, "waiter did not enqueue")
	}

	release()
	for i := 1; i <= 3; i++ {
		select {
		case got := <-order:
			if got != i {
				t.Fatalf("expected FIFO grant %d, got %d", i, got)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("waiter %d was never granted", i)
		}
		(<-releases)()
	}
}

func waitFor(t *testing.T, cond func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(message)
}
