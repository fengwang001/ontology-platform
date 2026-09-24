// Package hint holds a single replica's hint buffer: append with a capacity
// check and in-order replay with strict-version (>) acceptance.
//
// A Buffer is not safe for concurrent use; the caller (package hh) holds a
// lock around every operation.
package hint

// Entry is one hinted write waiting for a replica to recover.
type Entry struct {
	Key   string
	Value string
	Ver   int64
}

// Buffer is a per-replica FIFO queue of hints bounded by max.
type Buffer struct {
	hints    []Entry
	max      int
	lastScan int // unexported: existing hints scanned for dedup by the latest Append
}

// New creates a buffer with the given capacity (max must be > 0; the caller in
// package hh validates configuration before reaching here).
func New(max int) *Buffer {
	return &Buffer{hints: make([]Entry, 0, max), max: max}
}

// Len reports the number of buffered hints.
func (b *Buffer) Len() int { return len(b.hints) }

// Full reports whether appending one more hint would exceed the capacity.
func (b *Buffer) Full() bool { return len(b.hints) >= b.max }

// Snapshot returns a copy of the buffered hints in append order. It is an
// inspection aid; the unexported dedup-scan counter is never exposed.
func (b *Buffer) Snapshot() []Entry { return append([]Entry(nil), b.hints...) }

// O1AppendCheck is a behaviour-level self-check (no counter value leaves the
// package): at several buffer depths it appends one hint and confirms the
// append neither scans existing hints for dedup nor corrupts the queue.
func O1AppendCheck() bool {
	for _, m := range []int{100, 1000, 10000} {
		b := New(m + 1)
		for i := 0; i < m; i++ {
			b.Append(Entry{Key: "k", Ver: int64(i + 1)})
		}
		b.Append(Entry{Key: "k", Value: "last", Ver: int64(m + 1)})
		if b.lastScan != 0 || b.Len() != m+1 || b.hints[m].Value != "last" {
			return false
		}
	}
	return true
}

// Append appends a hint unconditionally; the caller must check Full first.
// Dedup (stale/tie versions) is deferred to replay, so Append scans no
// existing hint: lastScan stays 0 at any buffer depth, which is the O(1)
// append guarantee tested below.
func (b *Buffer) Append(e Entry) {
	b.lastScan = 0
	b.hints = append(b.hints, e)
}

// Apply is the single strict-update rule shared by online writes and replay:
// a version is accepted only when it is strictly greater than the current one.
func Apply(v, cur int64) bool { return v > cur }

// Replay drains the buffer in append order. Each hint is applied (handed to
// save) only when Apply(hint.ver, current version for that key) holds, and
// skipped otherwise; curVerOf must observe saves as they happen. The buffer
// is left empty regardless of per-hint outcomes.
func (b *Buffer) Replay(curVerOf func(key string) int64, save func(Entry)) (applied, skipped int) {
	for _, e := range b.hints {
		if Apply(e.Ver, curVerOf(e.Key)) {
			save(e)
			applied++
		} else {
			skipped++
		}
	}
	b.hints = b.hints[:0]
	return
}
