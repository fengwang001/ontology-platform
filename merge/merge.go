// Package merge applies a single delta entry onto a base value.
// It depends on no other package.
package merge

// Entry is one delta log record: a write (Val) or a delete tombstone (Del).
type Entry struct {
	Key string
	Val string
	Del bool
}

// Result is the merged value of one key: Ok=false means "absent".
type Result struct {
	Val string
	Ok  bool
}

// Apply folds one entry for its key onto the current base result.
// Entries for other keys must not be passed in by the caller.
// A tombstone makes the key absent; a write overwrites with Val.
func Apply(cur Result, e Entry) Result {
	if e.Del {
		return Result{Val: "", Ok: false}
	}
	return Result{Val: e.Val, Ok: true}
}
