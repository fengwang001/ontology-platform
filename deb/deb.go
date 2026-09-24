// Package deb holds the per-key pending batch: merge rules, due-time
// decision and the maintenance of batch count and latest value.
// It depends on no other package.
package deb

// Batch is one key's not-yet-refreshed batch.
type Batch struct {
	N   int64  // number of changes merged into this batch
	Val string // latest value wins
	Due int64  // logical time at which the batch may fire
}

// NewBatch starts a batch from the first change of a key: one record,
// value val, due at t+w.
func NewBatch(t, w int64, val string) Batch {
	return Batch{N: 1, Val: val, Due: t + w}
}

// Merge folds one more change into an open batch: count grows by one,
// the value is replaced (equal-t ties therefore go to the later arrival)
// and the due time is postponed to t+w.
func (b *Batch) Merge(t, w int64, val string) {
	b.N++
	b.Val = val
	b.Due = t + w
}

// IsDue reports whether the batch may fire at logical time now.
// Equality fires: a batch whose Due == now is due.
func (b Batch) IsDue(now int64) bool { return b.Due <= now }

// Take returns the batch payload (count, value) and resets it to empty.
func (b *Batch) Take() (n int64, val string) {
	n, val = b.N, b.Val
	*b = Batch{}
	return n, val
}
