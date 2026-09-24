// Package req defines a write request and its per-caller result.
package req

import "errors"

// Sentinel errors. All failure classes are matchable with errors.Is.
var (
	// ErrClosed means the committer was closed before the request was durable.
	ErrClosed = errors.New("committer closed")
	// ErrWrite means the batch write to the log failed.
	ErrWrite = errors.New("wal write failed")
	// ErrSync means the durability flush (fsync) failed.
	ErrSync = errors.New("wal sync failed")

	// ErrHeaderIncomplete: truncated before the fixed file header.
	ErrHeaderIncomplete = errors.New("file header incomplete")
	// ErrBatchHeaderIncomplete: a batch header is partially present.
	ErrBatchHeaderIncomplete = errors.New("batch header incomplete")
	// ErrEntryIncomplete: declared entries are partially present.
	ErrEntryIncomplete = errors.New("batch entries incomplete")
	// ErrCRCMismatch: the batch is whole but its batch-level CRC is wrong.
	ErrCRCMismatch = errors.New("batch crc mismatch")
)

// Request is one inbound write.
type Request struct {
	// ID is the caller-chosen correlation key; it is echoed in the Result.
	ID uint64
	// Payload is the raw bytes. An empty slice is a legal request.
	Payload []byte
}

// Size reports the on-log entry size (4-byte length prefix plus payload).
func (r Request) Size() int { return 4 + len(r.Payload) }

// Result is delivered to exactly the channel of the originating caller.
type Result struct {
	ID uint64
	// Seq is the global, zero-based sequence number on success.
	Seq uint64
	// Err is one of the package sentinel errors on failure, nil on success.
	Err error
}

// OK reports whether the request durably committed.
func (r Result) OK() bool { return r.Err == nil }
