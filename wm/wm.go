// Package wm validates a single batch and decides per-record application
// against a per-partition high watermark. It depends on nothing else.
package wm

import "errors"

// Rec is one upstream log record.
type Rec struct {
	Partition int
	Offset    int64
	Key       string
	Val       int64
}

var (
	// ErrInvalidRecord: negative partition/offset or empty key.
	ErrInvalidRecord = errors.New("wm: invalid record")
	// ErrOutOfOrder: offsets not strictly increasing within a batch per partition.
	ErrOutOfOrder = errors.New("wm: out-of-order offset within batch")
)

// Validate checks record legality first, then per-partition strictly
// increasing offsets; only the first error class is reported.
func Validate(batch []Rec) error {
	for _, r := range batch {
		if r.Partition < 0 || r.Offset < 0 || r.Key == "" {
			return ErrInvalidRecord
		}
	}
	last := make(map[int]int64)
	for _, r := range batch {
		if prev, ok := last[r.Partition]; ok && r.Offset <= prev {
			return ErrOutOfOrder
		}
		last[r.Partition] = r.Offset
	}
	return nil
}

// Apply reports whether a record at offset advances watermark w:
// exactly offset > w; anything at or below is a duplicate.
func Apply(w, offset int64) bool { return offset > w }
