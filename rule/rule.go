// Package rule holds the pure decision rules for version-conditioned writes.
// It depends on no other package in this module.
package rule

// Current returns a key's current version cur.
// A live row wins over a tombstone; if neither exists the result is 0.
func Current(live bool, liveVer int64, hasTomb bool, tombVer int64) int64 {
	switch {
	case live:
		return liveVer
	case hasTomb:
		return tombVer
	default:
		return 0
	}
}

// Applies reports whether an event at version ver applies against cur.
// The comparison is strictly greater: equal versions are ignored.
func Applies(ver, cur int64) bool { return ver > cur }

// Expired reports whether a tombstone at version t must be purged now,
// given the global watermark G and version-based retention R: G-t >= R.
func Expired(g, t, r int64) bool { return g-t >= r }
