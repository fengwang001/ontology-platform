package merge3

import "slices"

// Conflict describes one minimal conflicting region of a merge.
type Conflict struct {
	// At is the index into Result.Lines where the conflicting
	// content would be inserted.
	At int
	// Base, Ours and Theirs hold the divergent lines of each side.
	// Any of them may be empty (e.g. Base is empty for
	// insert-versus-insert conflicts).
	Base   []string
	Ours   []string
	Theirs []string
}

// Result is the outcome of Merge.
type Result struct {
	// Lines holds the cleanly merged lines in order. Lines of
	// conflicting regions are not included; they are available in
	// Conflicts. Use Render for a diff3-style annotated output.
	Lines []string
	// Conflicts lists every conflicting region, ordered by At.
	Conflicts []Conflict
}

// HasConflict reports whether the merge produced any conflict.
func (r Result) HasConflict() bool { return len(r.Conflicts) > 0 }

// Merge three-way merges ours and theirs against their common ancestor
// base. All inputs are lines without trailing newlines.
func Merge(base, ours, theirs []string) (Result, error) {
	keepO, insO := diffLines(base, ours)
	keepT, insT := diffLines(base, theirs)

	var r Result
	prev := -1 // previous base index stable in both sides
	// flush resolves the changed region (prev, q]: base lines
	// prev+1..q-1 plus insertions anchored at prev+1..q.
	flush := func(q int) {
		b := slices.Clone(base[prev+1 : q])
		o := sideSlice(base, keepO, insO, prev, q)
		t := sideSlice(base, keepT, insT, prev, q)
		r.resolve(b, o, t)
	}
	for i := 0; i < len(base); i++ {
		if keepO[i] && keepT[i] {
			flush(i)
			r.Lines = append(r.Lines, base[i])
			prev = i
		}
	}
	flush(len(base))
	return r, nil
}

// sideSlice reconstructs one side's lines for the region (prev, q]:
// insertions anchored at a, followed by base[a] if that side kept it.
func sideSlice(base []string, keep []bool, ins [][]string, prev, q int) []string {
	var out []string
	for a := prev + 1; a <= q; a++ {
		out = append(out, ins[a]...)
		if a < q && keep[a] {
			out = append(out, base[a])
		}
	}
	return out
}

// resolve merges one changed region into r.
func (r *Result) resolve(b, o, t []string) {
	switch {
	case slices.Equal(o, t):
		// Both sides made the same change (or no change).
		r.Lines = append(r.Lines, o...)
	case slices.Equal(o, b):
		r.Lines = append(r.Lines, t...)
	case slices.Equal(t, b):
		r.Lines = append(r.Lines, o...)
	default:
		// Genuine conflict: strip the prefix and suffix that both
		// sides agree on so only the divergent middle conflicts.
		pre := commonPrefix(o, t)
		suf := commonSuffix(o[pre:], t[pre:])
		r.Lines = append(r.Lines, o[:pre]...)
		r.Conflicts = append(r.Conflicts, Conflict{
			At:     len(r.Lines),
			Base:   b,
			Ours:   slices.Clone(o[pre : len(o)-suf]),
			Theirs: slices.Clone(t[pre : len(t)-suf]),
		})
		r.Lines = append(r.Lines, o[len(o)-suf:]...)
	}
}

func commonPrefix(a, b []string) int {
	n := min(len(a), len(b))
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

func commonSuffix(a, b []string) int {
	n := min(len(a), len(b))
	i := 0
	for i < n && a[len(a)-1-i] == b[len(b)-1-i] {
		i++
	}
	return i
}
