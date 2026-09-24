// Package wmline holds a single event-time watermark line:
//
//	wm = maxSeen - delay
//
// where maxSeen is the largest event timestamp observed so far. Lateness is
// decided strictly against the watermark established by earlier events: an
// event is late iff ts < wm (ts == wm is on time). Before any event arrived
// the watermark is negative infinity, so no event can ever be late.
package wmline

import "math"

// negInf is the initial maxSeen/watermark. It is also returned by WM before
// the first event. math.MinInt64 is unreachable by realistic timestamps and
// lets subtraction stay in normal int64 range.
const negInf = math.MinInt64

// Line is the fixed-delay watermark state machine. Delay adjustments, if any,
// are applied from outside via SetDelay; Line itself never changes delay.
type Line struct {
	maxSeen int64
	wm      int64
	delay   int64
}

// NewLine returns a line with the given (non-negative) delay and negative
// infinity maxSeen/watermark. Parameter validation is the caller's job.
func NewLine(delay int64) *Line {
	return &Line{maxSeen: negInf, wm: negInf, delay: delay}
}

// Observe applies one event and reports whether it was late. Lateness is
// judged first, against the previous watermark; only then maxSeen and wm
// advance, so the deciding event can never mark itself late.
func (l *Line) Observe(ts int64) bool {
	late := l.wm != negInf && ts < l.wm
	if ts > l.maxSeen {
		l.maxSeen = ts
	}
	l.wm = l.maxSeen - l.delay
	return late
}

// WM returns the current watermark, or math.MinInt64 before the first event.
func (l *Line) WM() int64 { return l.wm }

// Delay returns the current delay.
func (l *Line) Delay() int64 { return l.delay }

// SetDelay replaces the delay and takes effect from the next Observe onward.
func (l *Line) SetDelay(d int64) { l.delay = d }
