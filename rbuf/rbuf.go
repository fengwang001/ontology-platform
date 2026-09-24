// Package rbuf maintains buffered events in (TS, Seq) order and releases
// the ordered prefix whose TS is at or below a watermark. Depends on order.
package rbuf

import (
	"errors"
	"sort"

	"ontology/order"
)

// ErrFull is returned by Add when the buffer already holds max events.
var ErrFull = errors.New("rbuf: buffer full")

// Event is one buffered event. Seq is the arrival sequence number.
type Event struct {
	ID  string
	TS  int64
	Seq int64
}

// Buffer keeps events sorted by (TS, Seq). It is not goroutine-safe;
// callers (package api) serialize access.
type Buffer struct {
	max     int
	evs     []Event // sorted by (TS, Seq), ascending
	checked int     // events inspected during the most recent Release
}

// New returns a Buffer holding at most max events.
func New(max int) *Buffer { return &Buffer{max: max} }

// Len reports the number of buffered events.
func (b *Buffer) Len() int { return len(b.evs) }

// Add inserts e in (TS, Seq) order. A full buffer fails atomically:
// nothing is mutated.
func (b *Buffer) Add(e Event) error {
	if len(b.evs) >= b.max {
		return ErrFull
	}
	i := sort.Search(len(b.evs), func(i int) bool {
		return order.Less(e.TS, e.Seq, b.evs[i].TS, b.evs[i].Seq)
	})
	b.evs = append(b.evs, Event{})
	copy(b.evs[i+1:], b.evs[i:])
	b.evs[i] = e
	return nil
}

// Release removes every buffered event with TS <= wm and returns them in
// (TS, Seq) order. Because evs is sorted, only the released prefix plus at
// most one further event is inspected — never a full scan.
func (b *Buffer) Release(wm int64) []Event {
	b.checked = 0
	n := 0
	for n < len(b.evs) {
		b.checked++
		if b.evs[n].TS > wm {
			break
		}
		n++
	}
	out := make([]Event, n)
	copy(out, b.evs[:n])
	b.evs = b.evs[n:]
	return out
}
