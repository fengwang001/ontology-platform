package export

// Deduper merges repeated submissions (retries) of the same write inside one
// unconfirmed cycle.
//
// Position rule: a write keeps the slot of its FIRST acceptance for the whole
// cycle. A retry arriving after other writes neither re-inserts nor moves the
// write, so output is exactly the first-acceptance sequence with duplicates
// collapsed.
//
// Cost rule: membership is a hash table keyed by Write.ID. Each Accept costs
// O(1) average; it never scans the history already exported by this link. The
// table spans only the current, unconfirmed cycle — a finite window — and is
// discarded once the end is confirmed, so its size does not grow with the
// link's total history.
type Deduper struct {
	seen map[string]struct{}
	out  []Write
}

// NewDeduper creates an empty deduper for one cycle.
func NewDeduper() *Deduper {
	return &Deduper{seen: map[string]struct{}{}}
}

// Accept merges one submission. It returns the de-duplicated, first-acceptance
// sequence accumulated so far, and reports whether this is a fresh write.
// Retries of an already accepted ID return the existing sequence unchanged.
func (d *Deduper) Accept(w Write) (merged []Write, fresh bool) {
	if _, ok := d.seen[w.ID]; ok {
		return d.out, false
	}
	d.seen[w.ID] = struct{}{}
	d.out = append(d.out, w)
	return d.out, true
}

// Merged returns a copy of the de-duplicated writes in first-acceptance order.
func (d *Deduper) Merged() []Write {
	out := make([]Write, len(d.out))
	copy(out, d.out)
	return out
}
