// Package fanout fans one aggregate query out to many shards concurrently,
// bounding in-flight requests and honoring an overall deadline. Partial
// failures are returned as per-shard states rather than aborting the query.
package fanout

import "ontology/shard"

// ShardState is the terminal state of one shard in a run.
type ShardState struct {
	ID        string
	Status    shard.Status
	Frame     shard.Frame // valid when Status == StatusOK
	DupFrames []shard.Frame
	Err       error
}

// OK reports whether the shard's first frame was accepted.
func (s ShardState) OK() bool { return s.Status == shard.StatusOK }

// Accepted reports whether the shard produced a usable first frame.
// A shard whose later frames were duplicates still counts as accepted.
func (s ShardState) Accepted() bool {
	return s.Status == shard.StatusOK || s.Status == shard.StatusDuplicate
}

// Result is the deterministic, arrival-order-independent output of a run.
// States is sorted by Shard ID; Missing lists ids of non-OK shards in
// ascending order.
type Result struct {
	States  []ShardState
	Missing []string

	peakInflight int
	started      int
}

// Frames returns accepted frames in ascending shard-id order.
func (r *Result) Frames() []shard.Frame {
	out := make([]shard.Frame, 0, len(r.States))
	for _, st := range r.States {
		if st.Accepted() {
			out = append(out, st.Frame)
		}
	}
	return out
}

// PeakInflight reports the historical maximum number of concurrently
// in-flight shard requests observed during the run.
func (r *Result) PeakInflight() int { return r.peakInflight }

// Started reports how many shard requests were actually started
// (zero after the deadline for shards that never got a slot in time).
func (r *Result) Started() int { return r.started }
