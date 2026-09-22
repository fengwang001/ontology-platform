package unknown

import "sort"

// Sort rearranges retained fields into canonical order: ascending
// field number, with a stable tie-break so that repeated occurrences
// of the same number keep their original relative order.
//
// Parsing never calls Sort: byte-for-byte round trips require input
// order. It exists for producers that construct messages directly
// and want deterministic output.
func (f *Fields) Sort() {
	sort.SliceStable(f.items, func(i, j int) bool {
		return f.items[i].Number < f.items[j].Number
	})
}
