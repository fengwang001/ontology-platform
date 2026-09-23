// Package shard defines the shard interface, response frames and an
// injectable fake shard used to simulate latency, timeout, corruption and
// duplicate delivery without any real network.
package shard

import "errors"

// Sentinel errors for the four injectable failure classes. They are also
// surfaced as per-frame status values and can be matched with errors.Is.
var (
	ErrTimeout   = errors.New("shard: request timed out or canceled")
	ErrCorrupt   = errors.New("shard: record count does not match claimed count")
	ErrDuplicate = errors.New("shard: duplicate frame from same shard id")
	ErrNoResults = errors.New("shard: no successful shard results")
)

// Status is the per-shard outcome recorded in the final report.
type Status int

const (
	StatusUnknown   Status = iota
	StatusOK               // first frame accepted
	StatusDuplicate        // a later duplicate frame was discarded
	StatusCorrupt          // frame failed the claimed-count contract
	StatusTimeout          // deadline/cancel before any accepted frame
	StatusFailed           // other explicit failure
)

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusDuplicate:
		return "duplicate"
	case StatusCorrupt:
		return "corrupt"
	case StatusTimeout:
		return "timeout"
	case StatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Record is one data item. Pointer fields encode partial-field responses:
// a nil pointer means the shard did not provide that field.
type Record struct {
	ID    string
	N     *int64
	Score *int64
}

// Frame is one delivery from a shard.
type Frame struct {
	ShardID  string
	Claimed  int
	Records  []Record
	MaxScore int64 // shard-provided upper bound on every record's Score
}
