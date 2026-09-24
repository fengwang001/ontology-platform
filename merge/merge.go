// Package merge performs the two-pointer sorted-snapshot merge diff.
// It depends only on snap.
package merge

import (
	"errors"
	"reflect"
	"sort"

	"ontology/snap"
)

// Change is one changelog entry; Kind is 'I' insert, 'D' delete, 'U' update.
type Change struct {
	Kind     byte
	Key      int64
	Old, New string
}

// differ holds the unexported comparison counter of the latest run. It is
// reachable only inside this package and is never returned by any API.
type differ struct{ cmp int }

// Diff returns the changelog from oldRows to newRows (both strictly sorted).
func Diff(oldRows, newRows []snap.Row) []Change {
	var d differ
	return d.run(oldRows, newRows)
}

func (d *differ) run(o, n []snap.Row) []Change {
	ch := []Change{}
	i, j := 0, 0
	for i < len(o) && j < len(n) { // exactly one 3-way comparison per step
		d.cmp++
		switch {
		case o[i].Key < n[j].Key:
			ch = append(ch, Change{Kind: 'D', Key: o[i].Key, Old: o[i].Val})
			i++
		case o[i].Key > n[j].Key:
			ch = append(ch, Change{Kind: 'I', Key: n[j].Key, New: n[j].Val})
			j++
		default: // equal keys: emit U only when the value changed
			if o[i].Val != n[j].Val {
				ch = append(ch, Change{Kind: 'U', Key: o[i].Key, Old: o[i].Val, New: n[j].Val})
			}
			i++
			j++
		}
	}
	for ; i < len(o); i++ { // tail: a side is exhausted, no comparisons
		ch = append(ch, Change{Kind: 'D', Key: o[i].Key, Old: o[i].Val})
	}
	for ; j < len(n); j++ {
		ch = append(ch, Change{Kind: 'I', Key: n[j].Key, New: n[j].Val})
	}
	return ch
}

// Replay applies a changelog onto a fresh copy of base and returns the
// resulting snapshot (the replay half of invariant 1).
func Replay(base []snap.Row, ch []Change) []snap.Row {
	m := make(map[int64]string, len(base))
	for _, r := range base {
		m[r.Key] = r.Val
	}
	for _, c := range ch {
		if c.Kind == 'D' {
			delete(m, c.Key)
		} else {
			m[c.Key] = c.New
		}
	}
	out := make([]snap.Row, 0, len(m))
	for k, v := range m {
		out = append(out, snap.Row{Key: k, Val: v})
	}
	if len(out) == 0 {
		return nil // nil and empty snapshots must compare equal
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Naive is the reference diff: map both sides, walk the key union in
// ascending order, classify D / I / U (invariant 2).
func Naive(o, n []snap.Row) []Change {
	mo, mn := map[int64]string{}, map[int64]string{}
	for _, r := range o {
		mo[r.Key] = r.Val
	}
	for _, r := range n {
		mn[r.Key] = r.Val
	}
	seen, all := map[int64]bool{}, make([]int64, 0, len(o)+len(n))
	for _, m := range []map[int64]string{mo, mn} {
		for k := range m {
			if !seen[k] {
				seen[k], all = true, append(all, k)
			}
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	out := []Change{}
	for _, k := range all {
		vo, io := mo[k]
		vn, in := mn[k]
		switch {
		case io && !in:
			out = append(out, Change{'D', k, vo, ""})
		case !io && in:
			out = append(out, Change{'I', k, "", vn})
		case vo != vn:
			out = append(out, Change{'U', k, vo, vn})
		}
	}
	return out
}

// span builds rows with keys lo..hi, all sharing one value.
func span(lo, hi int64) []snap.Row {
	out := make([]snap.Row, 0, hi-lo+1)
	for k := lo; k <= hi; k++ {
		out = append(out, snap.Row{Key: k, Val: "v"})
	}
	return out
}

// SelfCheck verifies the section-3 changelog/comparison count and the
// disjoint-input count without exposing the counter value.
func SelfCheck() error {
	r := func(k int64, v string) snap.Row { return snap.Row{Key: k, Val: v} }
	o := []snap.Row{r(1, "a"), r(3, "b"), r(4, "c"), r(7, "d"), r(9, "e")}
	n := []snap.Row{r(2, "x"), r(3, "b"), r(4, "C"), r(5, "y"),
		r(9, "e"), r(10, "z"), r(12, "w")}
	var d differ
	want := []Change{{'D', 1, "a", ""}, {'I', 2, "", "x"}, {'U', 4, "c", "C"}, {'I', 5, "", "y"},
		{'D', 7, "d", ""}, {'I', 10, "", "z"}, {'I', 12, "", "w"}}
	if ch := d.run(o, n); d.cmp != 7 || !reflect.DeepEqual(ch, want) {
		return errors.New("merge: section-3 self-check failed")
	}
	for _, sz := range []int64{1, 100, 1000} { // disjoint: n old then m new
		var e differ
		e.run(span(1, sz), span(sz+1, 2*sz))
		if e.cmp != int(sz) {
			return errors.New("merge: disjoint comparison-count self-check failed")
		}
	}
	return nil
}
