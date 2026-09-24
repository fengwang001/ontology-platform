// Package audit checks that a sequence of pages covers a universe of
// rows exactly once and in sorted order: no duplicates, no omissions.
package audit

import (
	"errors"
	"fmt"
	"sort"

	"ontology/row"
)

// ErrMismatch is returned (wrapped) when a page sequence fails audit.
var ErrMismatch = errors.New("audit: page sequence mismatch")

// Verify concatenates pages and compares the combined sequence with
// the universe sorted by the composite key. It reports the first
// discrepancy found: a duplicated row, a missing row, or a row out
// of order.
func Verify(pages [][]row.Row, universe []row.Row) error {
	var got []row.Row
	for _, p := range pages {
		got = append(got, p...)
	}
	want := make([]row.Row, len(universe))
	copy(want, universe)
	sort.Slice(want, func(i, j int) bool { return row.Less(want[i], want[j]) })

	seen := map[string]int{}
	for _, r := range got {
		seen[r.ID]++
		if seen[r.ID] == 2 {
			return fmt.Errorf("%w: row %q returned twice", ErrMismatch, r.ID)
		}
	}
	inGot := map[string]bool{}
	for _, r := range got {
		inGot[r.ID] = true
	}
	for _, r := range want {
		if !inGot[r.ID] {
			return fmt.Errorf("%w: row %q missing", ErrMismatch, r.ID)
		}
	}
	if len(got) != len(want) {
		return fmt.Errorf("%w: got %d rows, want %d", ErrMismatch, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			return fmt.Errorf("%w: position %d: got %q, want %q",
				ErrMismatch, i, got[i].ID, want[i].ID)
		}
	}
	return nil
}
