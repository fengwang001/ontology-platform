// Package wtm holds the pure idle-timeout watermark rules: advancing a
// watermark by max (it only moves forward), the processing-time idle
// test, and the strict late-event test. It keeps no state and depends
// on no other package in this module.
package wtm

import "math"

// NegInf is negative infinity: the initial watermark and the initial
// last-event processing time before the first Feed.
const NegInf = math.MinInt64

// Advance returns the greater of cur and contrib, so a watermark can
// never retreat.
func Advance(cur, contrib int64) int64 {
	if contrib > cur {
		return contrib
	}
	return cur
}

// Mark is one observation's watermark contribution: ts-delay.
func Mark(ts, delay int64) int64 {
	return ts - delay
}

// Idle reports whether processing time pt is at least timeout past
// lastEventPT. The judgment uses processing time only. A stream that
// has never seen an event (lastEventPT == NegInf) is always idle; the
// sentinel is handled explicitly so that pt-NegInf cannot overflow.
func Idle(pt, lastEventPT, timeout int64) bool {
	if lastEventPT == NegInf {
		return true
	}
	return pt-lastEventPT >= timeout
}

// Late reports whether contribution mark is strictly behind wm.
// Equality is accepted: mark == wm is on time, not late.
func Late(mark, wm int64) bool {
	return mark < wm
}
