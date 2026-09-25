// Package seq tracks a single sender: next expected sequence number,
// out-of-order buffer, delivered log and duplicate count.
package seq

// Tracker holds the per-sender reception state. Not goroutine-safe;
// synchronization is the caller's (rb) responsibility.
type Tracker struct {
	next    int64         // next expected seq
	buf     map[int64]any // out-of-order arrivals with seq > next
	log     []any         // delivered data, strictly ascending seq from 0
	dups    int           // duplicate arrivals (already delivered or buffered)
	checked int           // entries inspected for the dedup/buffer decision in the last Add
}

// New returns an empty Tracker expecting seq 0.
func New() *Tracker { return &Tracker{buf: make(map[int64]any)} }

// Next returns the next expected seq.
func (t *Tracker) Next() int64 { return t.next }

// Buffered returns the number of entries in the out-of-order buffer.
func (t *Tracker) Buffered() int { return len(t.buf) }

// Has reports whether seq s is currently in the buffer.
func (t *Tracker) Has(s int64) bool { _, ok := t.buf[s]; return ok }

// Dups returns the duplicate arrival count.
func (t *Tracker) Dups() int { return t.dups }

// Log returns a copy of the delivered log.
func (t *Tracker) Log() []any { return append([]any(nil), t.log...) }

// Add applies one arrival. The caller must have validated s >= 0 and
// the buffer limit. Dedup is decided by a single comparison against
// next (O(1)); the delivered log is never scanned.
func (t *Tracker) Add(s int64, d any) {
	t.checked = 1 // the comparison s vs next
	switch {
	case s < t.next: // already delivered: duplicate
		t.dups++
	case s == t.next: // deliver, then cascade through the buffer
		t.log = append(t.log, d)
		t.next++
		for {
			v, ok := t.buf[t.next]
			if !ok {
				break
			}
			t.checked++
			t.log = append(t.log, v)
			delete(t.buf, t.next)
			t.next++
		}
	default: // s > next: buffer unless already buffered
		if _, ok := t.buf[s]; ok {
			t.dups++
		} else {
			t.buf[s] = d
		}
	}
}
