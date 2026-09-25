// Package rec defines the change-log record type and the tombstone
// retention decision. It depends on no other package in this module.
package rec

// Rec is one change-log record grouped by Key.
type Rec struct {
	Key   string
	Value int
	TS    int64
	// Del true means this record is a tombstone (deletion marker).
	Del bool
}

// IsTomb reports whether r is a tombstone.
func IsTomb(r Rec) bool { return r.Del }

// Drop decides whether a record that has already survived folding for
// window [lo,hi) must be discarded under retention period R (R >= 0):
// a tombstone is dropped exactly when TS+R <= hi; a normal record is
// always retained. The window start lo is irrelevant here because the
// caller only passes records already known to be inside the window.
func Drop(r Rec, hi, R int64) bool {
	if !r.Del {
		return false
	}
	return r.TS+R <= hi
}
