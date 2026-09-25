// Package win provides tumbling-window interval arithmetic.
package win

// Window is a left-closed, right-open interval [Start, End).
type Window struct {
	Start, End int64
}

// Index returns k = floor(ts/w), the window containing ts.
// w must be positive. Floor (not truncation) is used, so the
// boundary ts = k*w belongs to window k, never to window k-1.
func Index(ts, w int64) int64 {
	k := ts / w
	if ts < 0 && ts%w != 0 {
		k--
	}
	return k
}

// Of returns the window [k*w, (k+1)*w).
func Of(k, w int64) Window {
	return Window{Start: k * w, End: (k + 1) * w}
}

// Closed reports whether the window is closed at watermark wm:
// closed iff End <= wm.
func (w Window) Closed(wm int64) bool {
	return w.End <= wm
}
