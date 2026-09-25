// Package wm tracks a single stream's event-time watermark: the maximum TS
// observed so far. Before the first event the watermark is "negative
// infinity", represented by the Seen()==false state.
package wm

// Watermark is one stream's high-water mark. The zero value is a stream that
// has never observed an event (negative infinity).
type Watermark struct {
	seen bool
	ts   int64
}

// Seen reports whether the stream has observed at least one event.
func (w *Watermark) Seen() bool { return w.seen }

// Get returns the watermark TS and whether it is defined. When ok is false
// the watermark is negative infinity.
func (w *Watermark) Get() (ts int64, ok bool) { return w.ts, w.seen }

// Late reports whether an event with timestamp t is strictly late against the
// current watermark: t < maxTS. An event at exactly the watermark TS is not
// late, and nothing is late before the first observed event.
func (w *Watermark) Late(t int64) bool { return w.seen && t < w.ts }

// Advance moves the watermark to max(current, t) and marks the stream seen.
// A late t (t < current max) leaves the watermark unchanged by construction.
func (w *Watermark) Advance(t int64) {
	if !w.seen || t > w.ts {
		w.ts, w.seen = t, true
	}
}
