// Package budget keeps a hard accounting of bytes held by in-flight
// work. It is not safe for concurrent use; callers must serialize access.
package budget

// Budget tracks used bytes against a fixed limit.
type Budget struct {
	limit int64
	used  int64
}

// New returns a Budget that allows at most limit bytes to be held.
func New(limit int64) *Budget { return &Budget{limit: limit} }

// Limit returns the configured byte limit.
func (b *Budget) Limit() int64 { return b.limit }

// Used returns the currently charged byte count.
func (b *Budget) Used() int64 { return b.used }

// TryCharge attempts to charge n bytes. On success it returns true and
// the charge is recorded; on failure it returns false and nothing changes.
func (b *Budget) TryCharge(n int64) bool {
	if n < 0 || b.used+n > b.limit {
		return false
	}
	b.used += n
	return true
}

// Release returns n previously charged bytes to the budget.
func (b *Budget) Release(n int64) {
	b.used -= n
	if b.used < 0 {
		b.used = 0
	}
}
