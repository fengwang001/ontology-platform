// Package batcher implements a message batcher that flushes on count,
// byte size, or age, with re-deliverable failed batches.
package batcher

import (
	"errors"
	"sync"
	"time"
)

// ErrTooLarge is returned by Add when a single item exceeds maxBytes.
var ErrTooLarge = errors.New("batcher: item exceeds max bytes")

// ErrClosed is returned by Add and Flush after Close.
var ErrClosed = errors.New("batcher: closed")

// Batch is a sealed group of messages ready for delivery.
type Batch struct {
	Seq   uint64
	Items []string
	Bytes int
}

// Sink consumes a batch; a non-nil error marks the batch as failed.
type Sink interface {
	Deliver(b Batch) error
}

// Batcher buffers messages and seals them into batches on count, byte,
// or age triggers. It is safe for concurrent use.
type Batcher struct {
	sink     Sink
	maxItems int
	maxBytes int
	maxAge   time.Duration
	now      func() time.Time

	mu      sync.Mutex
	items   []string
	bytes   int
	since   time.Time
	seq     uint64
	failed  []Batch
	closed  bool
	deliver sync.Mutex
}

// New builds a Batcher. A batch is sealed when any of maxItems, maxBytes,
// or maxAge (measured from the first buffered item, via now) is reached.
func New(s Sink, maxItems, maxBytes int, maxAge time.Duration, now func() time.Time) *Batcher {
	if now == nil {
		now = time.Now
	}
	return &Batcher{
		sink:     s,
		maxItems: maxItems,
		maxBytes: maxBytes,
		maxAge:   maxAge,
		now:      now,
	}
}
