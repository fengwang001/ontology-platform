// Package ckpt decides idempotency for a single offset and owns the
// contiguous-prefix advance rule for the committed checkpoint.
package ckpt

// Kind is the classification of an incoming offset.
type Kind int

const (
	// New is a brand-new event that must be buffered in pending.
	New Kind = iota
	// Persisted means offset <= cp: already durable, skip silently.
	Persisted
	// Inflight means the offset is already in pending: skip silently.
	Inflight
)

// Decide classifies offset against the committed checkpoint cp and
// whether the offset is already inflight (in pending). The two guards
// together provide exactly-once semantics: anything already persisted
// or already buffered is an idempotent no-op.
func Decide(offset, cp int64, inflight bool) Kind {
	if offset <= cp {
		return Persisted
	}
	if inflight {
		return Inflight
	}
	return New
}

// Advance returns the new checkpoint after consuming the contiguous
// prefix starting at cp+1. has reports whether an offset is pending.
// It stops at the first gap and never skips over it.
func Advance(cp int64, has func(offset int64) bool) int64 {
	for has(cp + 1) {
		cp++
	}
	return cp
}
