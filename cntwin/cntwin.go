// Package cntwin implements the pure arithmetic of count-based tumbling
// windows: window assignment, trigger test, lateness and drop decisions.
// It depends on nothing and holds no state.
package cntwin

// Window returns the index k of the window [k*size, (k+1)*size) holding pos,
// i.e. floor(pos/size). Requires pos >= 0 and size > 0.
func Window(pos, size int64) int64 { return pos / size }

// Triggered reports whether a window holding cnt accepted elements fires.
func Triggered(cnt, size int64) bool { return cnt == size }

// Late reports whether pos arrives after a larger position was already seen
// (wm is the maximum position seen so far, -1 when none).
func Late(pos, wm int64) bool { return pos < wm }

// Acceptable reports whether a late element at pos is still within the
// allowed lateness: pos >= wm-lateness (inclusive bound).
func Acceptable(pos, wm, lateness int64) bool { return pos >= wm-lateness }
