// Package win assigns event timestamps to tumbling windows and decides
// lateness and closure against a watermark. It depends on no other package.
package win

// Window is a left-closed, right-open interval [Start, End).
type Window struct {
	Start, End int64
}

// Of returns the size-w window containing ts. The window index k may be any
// integer, including negatives, so this uses floor division rather than Go's
// truncating division (for which -5/10 == 0 would misplace negative TS).
func Of(ts, w int64) Window {
	k := ts / w
	if ts < 0 && ts%w != 0 {
		k--
	}
	return Window{Start: k * w, End: k*w + w}
}

// Late reports whether an event belonging to this window must be dropped:
// the watermark has reached or passed the window end (wm >= End).
func (w Window) Late(wm int64) bool { return wm >= w.End }

// Closed reports whether this window's retained state must be cleared:
// the window end has been reached by the watermark (End <= wm).
func (w Window) Closed(wm int64) bool { return wm >= w.End }
