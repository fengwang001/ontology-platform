// Package shard defines the injectable shard interface and records used by the
// fan-out merger. No real network is involved: implementations deliver Events.
package shard

import "context"

// Record is one data row contributed by a shard. Missing fields are zero:
// Count drives the Count aggregate; Value drives Sum/Min/Max/TopK.
type Record struct {
	ID    string
	Count int64
	Value int64
}

// Event is one delivery on a shard's result stream.
type Event struct {
	Records []Record
	// Claimed is the number of records the shard claims this batch contains.
	Claimed int
	// Bound is the per-shard upper bound: every record Value is <= Bound.
	Bound int64
	// BoundKnown reports whether Bound is meaningful.
	BoundKnown bool
	// Err terminates the stream with a shard-level failure.
	Err error
}

// Shard is the injectable backend. Fetch returns a stream of events so that a
// shard may deliver twice (simulating overlapping retries). The channel is
// closed when the shard is finished.
type Shard interface {
	Fetch(ctx context.Context, id string) <-chan Event
}

// Status enumerates how a single shard attempt ended.
type Status int

const (
	// StatusNone: outcome slot unused.
	StatusNone Status = iota
	// StatusOK: exactly one valid batch accepted.
	StatusOK
	// StatusTimeout: no valid batch before the overall deadline.
	StatusTimeout
	// StatusCanceled: context canceled for a reason other than deadline.
	StatusCanceled
	// StatusCorrupt: delivered record count did not match the claim.
	StatusCorrupt
	// StatusDuplicate: a second valid batch arrived and was dropped.
	StatusDuplicate
	// StatusFailed: shard reported a terminal error.
	StatusFailed
)

// Sentinel errors; callers distinguish failure kinds via errors.Is.
var (
	ErrTimeout   = shardErr("shard: timeout")
	ErrCanceled  = shardErr("shard: canceled")
	ErrCorrupt   = shardErr("shard: corrupt payload")
	ErrDuplicate = shardErr("shard: duplicate delivery")
	ErrFailed    = shardErr("shard: backend failure")
)

type shardErr string

func (e shardErr) Error() string { return string(e) }

// Outcome is the per-shard terminal state recorded by the fan-out.
type Outcome struct {
	ShardID    string
	Status     Status
	Records    []Record
	Bound      int64
	BoundKnown bool
	Err        error
}

// OK reports whether the shard contributed exactly one valid batch.
func (o Outcome) OK() bool { return o.Status == StatusOK }

// String renders a status for reports.
func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusTimeout:
		return "timeout"
	case StatusCanceled:
		return "canceled"
	case StatusCorrupt:
		return "corrupt"
	case StatusDuplicate:
		return "duplicate"
	case StatusFailed:
		return "failed"
	default:
		return "none"
	}
}
