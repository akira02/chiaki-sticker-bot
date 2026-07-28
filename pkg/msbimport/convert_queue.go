package msbimport

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// Heavy converters (ffmpeg, rlottie, frame-extracting ImageMagick) share one
// queue so their memory peaks never sum past the small VM. Static image
// conversions get their own queue: they are cheap and already bounded by
// MSB_IM_MEMORY_LIMIT, and making them wait behind a multi-minute VP9 encode is
// what made a purely static import look frozen at "0 of N".
var (
	heavyQueue         = &converterQueue{name: "heavy converter"}
	staticImageQueue   = &converterQueue{name: "image converter"}
	converterQueueOnce sync.Once
)

// How long a slot wait may go unreported. Long waits are legitimate (someone
// else is encoding), but they must be visible in the log.
const converterQueueWarnInterval = 15 * time.Second

func initConverterQueues() {
	converterQueueOnce.Do(func() {
		heavyQueue.capacity = converterEnvInt("MSB_FFMPEG_CONCURRENCY", 1)
		// Escape hatch: if the extra concurrent ImageMagick ever costs too much
		// memory, fall back to serializing everything through one queue.
		if os.Getenv("MSB_IM_SHARE_HEAVY_SLOT") == "1" {
			staticImageQueue = heavyQueue
			return
		}
		staticImageQueue.capacity = converterEnvInt("MSB_IM_CONCURRENCY", 1)
	})
}

func converterEnvInt(key string, fallback int) int {
	if value, err := strconv.Atoi(os.Getenv(key)); err == nil && value > 0 {
		return value
	}
	return fallback
}

// Upper bound on a slot wait. Without it a wedged converter blocks every queued
// conversion forever, with no error and nothing in the log.
func converterQueueTimeout() time.Duration {
	return time.Duration(converterEnvInt("MSB_CONVERT_QUEUE_TIMEOUT_SECONDS", 600)) * time.Second
}

// converterQueue is a FIFO slot limiter. Unlike a bare channel semaphore it
// reports queue position, honours ctx cancellation and logs long waits, so a
// blocked conversion is diagnosable instead of silently stuck.
type converterQueue struct {
	name     string
	capacity int

	mu     sync.Mutex
	active int
	queue  []*converterQueueEntry
}

type converterQueueEntry struct {
	ready   chan struct{}
	status  *ConversionStatus
	granted bool
}

func (q *converterQueue) acquire(ctx context.Context, status *ConversionStatus) (func(), error) {
	initConverterQueues()
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	entry := &converterQueueEntry{ready: make(chan struct{}), status: status}
	if q.tryAcquire(entry) {
		return q.releaseOnce(), nil
	}

	since := time.Now()
	timeout := converterQueueTimeout()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	warn := time.NewTicker(converterQueueWarnInterval)
	defer warn.Stop()

	for {
		select {
		case <-entry.ready:
			status.ClearQueue()
			log.Infof("convert queue: %s slot granted after %s", q.name, time.Since(since).Truncate(time.Millisecond))
			return q.releaseOnce(), nil
		case <-warn.C:
			active, capacity, queued := q.stats()
			log.Warnf("convert queue: waiting %s for a %s slot (active %d/%d, %d queued)",
				time.Since(since).Truncate(time.Second), q.name, active, capacity, queued)
		case <-timer.C:
			if !q.cancel(entry) {
				status.ClearQueue()
				return q.releaseOnce(), nil
			}
			status.ClearQueue()
			log.Errorf("convert queue: gave up after %s waiting for a %s slot", timeout, q.name)
			return nil, fmt.Errorf("timed out after %s waiting for a %s slot", timeout, q.name)
		case <-ctx.Done():
			if !q.cancel(entry) {
				status.ClearQueue()
				return q.releaseOnce(), nil
			}
			status.ClearQueue()
			return nil, ctx.Err()
		}
	}
}

// releaseOnce guards against a caller releasing twice, which would inflate
// capacity and let concurrent encodes OOM the VM.
func (q *converterQueue) releaseOnce() func() {
	var once sync.Once
	return func() { once.Do(q.release) }
}

func (q *converterQueue) tryAcquire(entry *converterQueueEntry) bool {
	q.mu.Lock()
	if q.active < q.capacity && len(q.queue) == 0 {
		q.active++
		q.mu.Unlock()
		return true
	}
	q.queue = append(q.queue, entry)
	updates := q.queueStatusLocked()
	q.mu.Unlock()
	applyQueueStatus(updates)
	return false
}

func (q *converterQueue) release() {
	q.mu.Lock()
	if q.active > 0 {
		q.active--
	}
	for q.active < q.capacity && len(q.queue) > 0 {
		entry := q.queue[0]
		q.queue = q.queue[1:]
		q.active++
		entry.granted = true
		close(entry.ready)
	}
	updates := q.queueStatusLocked()
	q.mu.Unlock()
	applyQueueStatus(updates)
}

// cancel reports whether the entry was removed before being granted. A granted
// entry owns a slot, so its caller must release instead of walking away.
func (q *converterQueue) cancel(entry *converterQueueEntry) bool {
	q.mu.Lock()
	if entry.granted {
		q.mu.Unlock()
		return false
	}
	for i, queued := range q.queue {
		if queued == entry {
			q.queue = append(q.queue[:i], q.queue[i+1:]...)
			updates := q.queueStatusLocked()
			q.mu.Unlock()
			applyQueueStatus(updates)
			return true
		}
	}
	q.mu.Unlock()
	return true
}

func (q *converterQueue) stats() (active int, capacity int, queued int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.active, q.capacity, len(q.queue)
}

func (q *converterQueue) queueStatusLocked() []func() {
	updates := make([]func(), 0, len(q.queue))
	total := len(q.queue)
	for i, entry := range q.queue {
		if entry.status == nil {
			continue
		}
		status := entry.status
		text := fmt.Sprintf("queued / 排隊等待轉檔中... position %d of %d", i+1, total)
		updates = append(updates, func() { status.SetQueue(text) })
	}
	return updates
}

// Status updates run outside the queue lock: they are only string writes, but
// keeping them out avoids holding the lock across unrelated code.
func applyQueueStatus(updates []func()) {
	for _, update := range updates {
		update()
	}
}

func acquireFFmpegSlot(ctx context.Context, status *ConversionStatus) (func(), error) {
	return heavyQueue.acquire(ctx, status)
}

func acquireLottieGIFSlot(ctx context.Context, status *ConversionStatus) (func(), error) {
	return heavyQueue.acquire(ctx, status)
}

// heavy marks frame-extracting/coalescing work, which holds every frame in the
// pixel cache and therefore belongs in the same queue as ffmpeg. Single-image
// conversions go to the cheap queue.
func acquireImageMagickSlot(ctx context.Context, status *ConversionStatus, heavy bool) (func(), error) {
	initConverterQueues()
	if heavy {
		return heavyQueue.acquire(ctx, status)
	}
	return staticImageQueue.acquire(ctx, status)
}
