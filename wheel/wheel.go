// Package wheel implements a single wheel level: a fixed number of slots,
// a cursor derived from an externally injected clock, and flush-on-arrival
// semantics. Not goroutine-safe; callers synchronize.
package wheel

import "ontology/slot"

// Wheel is one level of a hierarchical timing wheel. Tick is the slot width
// in ticks (S^level); CurrentTime is the injected time aligned down to Tick.
type Wheel struct {
	slots       []slot.Slot
	Tick        int64
	CurrentTime int64
	cursor      int
}

// New creates a wheel with numSlots slots of width tick, aligned to start.
func New(numSlots int, tick, start int64) *Wheel {
	return &Wheel{
		slots:       make([]slot.Slot, numSlots),
		Tick:        tick,
		CurrentTime: start - start%tick,
	}
}

// Interval is the total range covered by one revolution, in ticks.
func (w *Wheel) Interval() int64 { return w.Tick * int64(len(w.slots)) }

// NumSlots returns the fixed slot count.
func (w *Wheel) NumSlots() int { return len(w.slots) }

// Cursor returns the current slot index.
func (w *Wheel) Cursor() int { return w.cursor }

// Slot exposes slot i for insertion and read-only inspection.
func (w *Wheel) Slot(i int) *slot.Slot { return &w.slots[i] }

// SlotIndex maps an expiration time to its slot index at this level.
func (w *Wheel) SlotIndex(exp int64) int {
	return int((exp / w.Tick) % int64(len(w.slots)))
}

// InsertAt puts v under handle into slot idx.
func (w *Wheel) InsertAt(idx int, handle uint64, v any) {
	w.slots[idx].Insert(handle, v)
}

// AdvanceTo moves CurrentTime forward to the largest Tick-aligned value
// <= now, if now has reached the next slot boundary. It empties the slot
// the cursor lands on and reports whether the wheel completed a full
// revolution (i.e. the next higher level must also advance).
func (w *Wheel) AdvanceTo(now int64) (flushed []slot.Entry, wrapped bool) {
	if now < w.CurrentTime+w.Tick {
		return nil, false
	}
	w.CurrentTime = now - now%w.Tick
	w.cursor = w.SlotIndex(w.CurrentTime)
	flushed = w.slots[w.cursor].TakeAll()
	wrapped = w.CurrentTime%w.Interval() == 0
	return flushed, wrapped
}
