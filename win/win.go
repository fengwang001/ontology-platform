// Package win owns event-time window assignment for tumbling windows.
// It depends on no other package in this module.
package win

// Window is the half-open interval [Start, End) with index K = Start/W.
type Window struct {
	K     int64
	Start int64
	End   int64
}

// Index returns k = floor(TS/W) for W > 0 and TS >= 0. The domain is
// k >= 0, so plain integer division is the floor division.
func Index(ts, w int64) int64 {
	return ts / w
}

// Of returns the half-open window [k*W, (k+1)*W) containing ts.
func Of(ts, w int64) Window {
	k := Index(ts, w)
	start := k * w
	return Window{K: k, Start: start, End: start + w}
}

// Closed reports whether a window ending at end is closed under watermark
// wm: exactly when end <= wm. Before the first event the watermark is
// negative infinity and the caller's open-window set is empty, so this is
// never consulted then.
func Closed(end, wm int64) bool { return end <= wm }
