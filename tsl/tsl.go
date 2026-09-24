// Package tsl holds the offset/timestamp relation and the incremental
// prefix-maximum update used by the bidirectional index. It depends on
// no other package in this module.
package tsl

// Entry is one appended record: its offset, its raw event timestamp and
// the running prefix maximum PM = max(ts[0..off]).
type Entry struct {
	Off int64
	TS  int64
	PM  int64
}

// First starts a prefix-maximum sequence at offset 0, where pm[0] = ts[0].
func First(ts int64) Entry {
	return Entry{Off: 0, TS: ts, PM: ts}
}

// Next extends the sequence by one record. The prefix maximum is updated
// incrementally as max(prev.PM, ts), which keeps the PM stream
// non-decreasing regardless of how the raw timestamps move.
func Next(prev Entry, ts int64) Entry {
	pm := ts
	if prev.PM > pm {
		pm = prev.PM
	}
	return Entry{Off: prev.Off + 1, TS: ts, PM: pm}
}

// Max returns the larger of two int64 timestamps.
func Max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
