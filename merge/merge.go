// Package merge folds a single delta log entry onto a merged base value.
// It depends on no other package.
package merge

// Entry is one delta log record in arrival order.
// Del=false: a write that overwrites the key with Val.
// Del=true:  a delete tombstone; the key becomes absent.
type Entry struct {
	Key string
	Val string
	Del bool
}

// Result is a merged lookup outcome.
// OK=false means the key does not exist (never "stored as empty").
type Result struct {
	Val string
	OK  bool
}

// Apply folds a single entry for its key into the current merged result.
// A tombstone yields the zero Result (absent); a write yields Val, present.
func Apply(cur Result, e Entry) Result {
	if e.Del {
		return Result{}
	}
	return Result{Val: e.Val, OK: true}
}
