// Package groupcommit implements a batch-write group committer.
//
// Many concurrent writers submit individual payloads; a single background
// worker collects them in FIFO order, assigns a contiguous run of sequence
// numbers, and persists each batch atomically through a caller-supplied
// Persister.
package groupcommit

import (
	"context"
)

// Config configures a GroupCommitter.
type Config struct {
	// MaxEntries is the maximum number of entries allowed in one batch.
	MaxEntries int
	// MaxBytes is the maximum summed payload byte size allowed in one batch.
	MaxBytes int
	// Persister atomically persists one batch.
	Persister Persister
	// Log receives structured diagnostic lines. nil disables logging.
	Log Logger
}

// Persister atomically persists a single batch of entries.
type Persister interface {
	Persist(ctx context.Context, entries [][]byte) error
}

// Logger is the minimal logging sink used by the committer.
type Logger interface {
	Printf(format string, args ...any)
}

// Result is delivered to the caller of a successful submit.
type Result struct {
	// Seq is the assigned, durable sequence number, starting at 1.
	Seq int64
	// Batch is the 1-based ordinal of the batch that persisted the entry.
	Batch int64
}

type pending struct {
	payload []byte
	result  chan batchResult
}

type batchResult struct {
	seq    int64
	batch  int64
	failed bool
	err    error
}

type noopLogger struct{}

func (noopLogger) Printf(format string, args ...any) {}

// GroupCommitter merges concurrent writes into serial, atomic batches.
type GroupCommitter struct {
	maxEntries int
	maxBytes   int
	persister  Persister
	log        Logger

	submit chan *pending
	done   chan struct{}

	shutdown chan struct{} // closed to signal the worker

	// batchGate, when non-nil, is awaited before each batch is taken. It is a
	// test hook for preloading the queue and making batch cuts deterministic.
	batchGate <-chan struct{}
}

// NewGroupCommitter constructs a committer and starts its background worker.
func NewGroupCommitter(cfg Config) (*GroupCommitter, error) {
	if cfg.MaxEntries <= 0 {
		return nil, ErrInvalidMaxEntries
	}
	if cfg.MaxBytes <= 0 {
		return nil, ErrInvalidMaxBytes
	}
	if cfg.Persister == nil {
		return nil, ErrNilPersister
	}
	log := cfg.Log
	if log == nil {
		log = noopLogger{}
	}
	gc := &GroupCommitter{
		maxEntries: cfg.MaxEntries,
		maxBytes:   cfg.MaxBytes,
		persister:  cfg.Persister,
		log:        log,
		submit:     make(chan *pending, 4096),
		done:       make(chan struct{}),
		shutdown:   make(chan struct{}),
	}
	go gc.run()
	return gc, nil
}

// Submit queues one payload and waits for the batch outcome.
//
// Rejection (empty payload, oversized payload, closed committer) happens
// before queuing and never consumes a sequence number. If ctx is cancelled
// while the request waits in a batch, the entry is still persisted with its
// batch; only this caller stops waiting for the result.
func (gc *GroupCommitter) Submit(ctx context.Context, payload []byte) (Result, error) {
	if len(payload) == 0 {
		gc.log.Printf("submit reject input=%d bytes reason=empty-payload", 0)
		return Result{}, ErrEmptyPayload
	}
	if len(payload) > gc.maxBytes {
		gc.log.Printf("submit reject input=%d bytes max=%d reason=payload-too-large", len(payload), gc.maxBytes)
		return Result{}, ErrPayloadTooLarge
	}

	// One-at-a-time gate against Close: either the request is handed to the
	// worker (which guarantees its result during shutdown) or rejected.
	select {
	case <-gc.shutdown:
		gc.log.Printf("submit reject input=%d bytes reason=closed", len(payload))
		return Result{}, ErrClosed
	default:
	}

	req := &pending{
		payload: payload,
		result:  make(chan batchResult, 1),
	}

	select {
	case gc.submit <- req:
	case <-gc.shutdown:
		// Close won the race; this request never reached the worker.
		gc.log.Printf("submit reject input=%d bytes reason=closed-during-queue", len(payload))
		return Result{}, ErrClosed
	}
	gc.log.Printf("submit accepted input=%d bytes", len(payload))

	select {
	case res := <-req.result:
		if res.failed {
			return Result{}, res.err
		}
		return Result{Seq: res.seq, Batch: res.batch}, nil
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}
