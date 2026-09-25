// Package diff computes column-level changes between two string rows.
// A row is map[string]string: column presence is map membership only;
// the empty string "" is a legitimate value and never means "missing".
package diff

import (
	"errors"
	"sort"
	"strconv"
	"strings"
)

// Kind identifies the kind of one column-level change.
type Kind int

const (
	Added Kind = iota + 1
	Removed
	Changed
)

func (k Kind) String() string {
	switch k {
	case Added:
		return "A"
	case Removed:
		return "R"
	default:
		return "C"
	}
}

// Change is one column-level change. For Removed, New is ""; for Added, Old
// is "". Those empties are placeholders for "absent on that side" only.
type Change struct {
	Kind Kind
	Col  string
	Old  string
	New  string
}

func (c Change) String() string {
	return c.Kind.String() + "{" + c.Col + "," + c.Old + "," + c.New + "}"
}

// Differ computes row diffs. The unexported compares field counts the value
// comparisons (before[col] == after[col]) performed during the last Diff:
// exactly one per column present on both sides. It is intentionally not
// reachable through any exported accessor.
type Differ struct {
	compares int
}

// New returns a ready Differ.
func New() *Differ { return &Differ{} }

// fingerprint returns the sorted column names and a collision-free canonical
// encoding of the row (length-prefixed pieces). Two rows are identical iff
// their fingerprints are equal; computing it performs no value comparisons.
func fingerprint(row map[string]string) ([]string, string) {
	keys := make([]string, 0, len(row))
	for k := range row {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(strconv.Itoa(len(k)))
		b.WriteByte('=')
		b.WriteString(k)
		v := row[k]
		b.WriteString(strconv.Itoa(len(v)))
		b.WriteByte('=')
		b.WriteString(v)
	}
	return keys, b.String()
}

// Diff returns the column-level changes from before to after, ordered by
// column name. Identical rows short-circuit on their fingerprint with zero
// value comparisons; otherwise both name lists are merged in one pass, so a
// column is value-compared at most once.
func (d *Differ) Diff(before, after map[string]string) []Change {
	d.compares = 0
	bk, bf := fingerprint(before)
	ak, af := fingerprint(after)
	if len(before) == len(after) && bf == af {
		return nil // whole-row fingerprint short-circuit: no per-column rescan
	}
	changes := make([]Change, 0)
	i, j := 0, 0
	for i < len(bk) || j < len(ak) {
		switch {
		case j == len(ak) || (i < len(bk) && bk[i] < ak[j]):
			changes = append(changes, Change{Kind: Removed, Col: bk[i], Old: before[bk[i]]})
			i++
		case i == len(bk) || (j < len(ak) && ak[j] < bk[i]):
			changes = append(changes, Change{Kind: Added, Col: ak[j], New: after[ak[j]]})
			j++
		default:
			col := bk[i]
			ov, nv := before[col], after[col]
			d.compares++ // the one and only value comparison for this column
			if ov != nv {
				changes = append(changes, Change{Kind: Changed, Col: col, Old: ov, New: nv})
			}
			i++
			j++
		}
	}
	return changes
}

// SelfCheck verifies the internal complexity guarantees at several sizes.
// It reports pass/fail only; the counter value itself is never exposed.
func SelfCheck() error {
	d := New()
	for _, m := range []int{100, 1000, 10000} {
		before := make(map[string]string, m)
		after := make(map[string]string, m)
		for i := 0; i < m; i++ {
			c := strconv.Itoa(i)
			before[c] = "b" + c
			after[c] = "a" + c
		}
		d.Diff(before, after)
		if d.compares != m { // one comparison per shared column: linear, not m^2
			return errors.New("diff: value comparisons are not one per column")
		}
		d.Diff(after, after)
		if d.compares != 0 { // identical rows short-circuit before any comparison
			return errors.New("diff: identical rows are not fingerprint-short-circuited")
		}
	}
	return nil
}
