// Package sess defines a single session interval and its predicates.
package sess

import "sort"

// Session is a contiguous run of events for one key: interval [Start, End]
// with Count events. Closed sessions are frozen forever.
type Session struct {
	Start  int64
	End    int64
	Count  int
	Closed bool
}

// InMergeSet reports whether ts lies in the session's gap neighbourhood,
// i.e. Start-gap <= ts <= End+gap.
func (s Session) InMergeSet(ts, gap int64) bool {
	return s.Start-gap <= ts && ts <= s.End+gap
}

// Closable reports whether watermark wm closes this session: wm > End+gap.
func (s Session) Closable(wm, gap int64) bool { return wm > s.End+gap }

// Link recomputes sessions from one key's accepted timestamps: sorted,
// neighbours with gap <= g join one run. Independent oracle for SelfCheck.
func Link(ts []int64, g int64) []Session {
	sorted := append([]int64(nil), ts...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var out []Session
	for _, t := range sorted {
		if n := len(out); n > 0 && t-out[n-1].End <= g {
			out[n-1].End = t
			out[n-1].Count++
		} else {
			out = append(out, Session{Start: t, End: t, Count: 1})
		}
	}
	return out
}
