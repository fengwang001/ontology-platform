// Package window holds the per-key sampling entry and the keep/drop decision.
package window

// AlwaysKeepSeverity: records with sev >= 4 are always kept and never
// consume the per-window quota.
const AlwaysKeepSeverity = 4

// Entry is the sampling state of one (tenant, key) fingerprint.
type Entry struct {
	Win     uint64 // window number this entry currently accounts for
	Cnt     uint64 // sev<4 records seen in Win
	Dropped uint64 // dropped records in Win not yet delivered in a summary
}

// Of returns the window number for a timestamp: cur = floor(now / W).
func Of(now, w uint64) uint64 { return now / w }

// Stale reports whether the entry belongs to a window other than cur.
func (e *Entry) Stale(cur uint64) bool { return e.Win != cur }

// Reset moves the entry into window cur, clearing counters. The caller must
// have accounted for any pending Dropped (e.g. via a Rolled summary) first.
func (e *Entry) Reset(cur uint64) {
	e.Win, e.Cnt, e.Dropped = cur, 0, 0
}

// Keep applies one record to the entry, which must already belong to the
// current window. It reports whether the record is kept.
//
// sev >= AlwaysKeepSeverity: always kept, Cnt and Dropped untouched.
// Otherwise Cnt is incremented; the record is kept when Cnt <= N, or when
// Cnt > N and (Cnt-N) is a multiple of M; else Dropped is incremented.
func (e *Entry) Keep(sev int, n, m uint64) bool {
	if sev >= AlwaysKeepSeverity {
		return true
	}
	e.Cnt++
	if e.Cnt <= n || (e.Cnt-n)%m == 0 {
		return true
	}
	e.Dropped++
	return false
}
