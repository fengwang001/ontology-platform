// Package hint holds a single replica's buffer of hinted writes.
//
// Dedup against stale or tied versions is deferred to replay time: Append is
// O(1) and never inspects existing hints. The unexported scans counter records
// how many existing hints the latest Append scanned for dedup — it is always
// zero, which the in-package test TestAppendScanIsO1 pins across buffer sizes.
package hint

import "errors"

// ErrFull is returned when an append would exceed the buffer's capacity.
var ErrFull = errors.New("hint: buffer full")

// Entry is one hinted write waiting to be replayed to a recovered replica.
type Entry struct {
	Key   string
	Value string
	Ver   int64
}

// Buffer is an append-only per-replica queue with a fixed entry cap.
type Buffer struct {
	hints []Entry
	max   int
	scans int // unexported: existing hints scanned by the latest Append (0)
}

// NewBuffer creates a buffer that holds at most maxHints entries.
func NewBuffer(maxHints int) *Buffer {
	return &Buffer{hints: make([]Entry, 0, maxHints), max: maxHints}
}

// Len reports how many hints are currently buffered.
func (b *Buffer) Len() int { return len(b.hints) }

// Snapshot returns a copy of buffered hints in append order.
func (b *Buffer) Snapshot() []Entry { return append([]Entry(nil), b.hints...) }

// WouldOverflow reports whether one more append would exceed the cap.
func (b *Buffer) WouldOverflow() bool { return len(b.hints) >= b.max }

// Append queues e without touching existing entries (no dedup scan, O(1)).
// It fails with ErrFull instead of mutating the buffer when the cap is hit.
func (b *Buffer) Append(e Entry) error {
	if b.WouldOverflow() {
		return ErrFull
	}
	b.scans = 0
	b.hints = append(b.hints, e)
	return nil
}

// ShouldApply is the single strict-update rule shared by online writes and
// replay: a version supersedes the replica only when strictly greater, so ties
// (去并列) and older versions (去旧) are both skipped.
func ShouldApply(ver, cur int64) bool { return ver > cur }

// Replay drains the buffer in append order. applied is called with each hint
// and must return whether that hint was newer than the replica's current
// state; skipped counts the rest. The buffer is emptied in every case.
func (b *Buffer) Replay(applied func(Entry) bool) (nApplied, nSkipped int) {
	for _, h := range b.hints {
		if applied(h) {
			nApplied++
		} else {
			nSkipped++
		}
	}
	b.hints = b.hints[:0]
	return nApplied, nSkipped
}
