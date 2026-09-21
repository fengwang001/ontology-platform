// Package batcher implements a message batcher with triple triggers
// (item count, byte count, and age) and redeliverable failed batches.
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

// SinkFunc adapts a function to Sink.
type SinkFunc func(b Batch) error

// Deliver calls f(b).
func (f SinkFunc) Deliver(b Batch) error { return f(b) }

// Batcher buffers messages and seals them into batches.
type Batcher struct {
	mu       sync.Mutex
	sink     Sink
	maxItems int
	maxBytes int
	maxAge   time.Duration
	now      func() time.Time

	items   []string
	bytes   int
	firstAt time.Time
	seq     uint64
	failed  []Batch
	closed  bool
}

// New creates a Batcher. A batch is sealed when any of maxItems,
// maxBytes, or maxAge is reached. now is an injectable clock;
// nil means time.Now.
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
