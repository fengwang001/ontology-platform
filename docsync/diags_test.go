package docsync

import (
	"sort"
	"testing"
)

func activeRanges(t *testing.T, st *Store) []Range {
	t.Helper()
	snap := st.Snapshot()
	out := make([]Range, 0, len(snap.Diagnostics))
	for _, d := range snap.Diagnostics {
		out = append(out, d.Range)
	}
	return out
}

func TestInsertionEndpointRelations(t *testing.T) {
	// Base text "abcdefgh"; insert "X" at offset 4 (between d and e).
	diags := map[string]Range{
		"before":  rng(0, 0, 0, 2), // end < s
		"a-is-s":  rng(0, 4, 0, 6), // start == s
		"b-is-s":  rng(0, 2, 0, 4), // end == s, non-empty
		"contain": rng(0, 2, 0, 6), // a < s < b
		"after":   rng(0, 6, 0, 7), // start > s
		"empty":   rng(0, 4, 0, 4), // empty at s
	}
	st := NewStore("abcdefgh")
	names := make([]string, 0, len(diags))
	for name, rg := range diags {
		names = append(names, name)
		if _, err := st.Register(0, Diagnostic{Range: rg, Message: name}); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
	sort.Strings(names)
	if _, err := st.Apply(0, []Edit{{Range: rng(0, 4, 0, 4), Text: "X"}}); err != nil {
		t.Fatal(err)
	}
	want := map[string]Range{
		"before":  rng(0, 0, 0, 2),
		"a-is-s":  rng(0, 5, 0, 7),
		"b-is-s":  rng(0, 2, 0, 4),
		"contain": rng(0, 2, 0, 7),
		"after":   rng(0, 7, 0, 8),
		"empty":   rng(0, 5, 0, 5),
	}
	got := map[string]Range{}
	for _, d := range st.Snapshot().Diagnostics {
		got[d.Message] = d.Range
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("diag %s = %+v want %+v", name, got[name], w)
		}
	}
}

func TestEmptyDiagnosticInsideDeletion(t *testing.T) {
	st := NewStore("abcdef")
	// Delete [2,5) ("cde"): result "abf". Track empty diags by id.
	orig := map[int]int{} // id -> original offset
	for _, off := range []int{0, 1, 2, 3, 5, 6} {
		id, err := st.Register(0, Diagnostic{Range: rng(0, off, 0, off), Message: itoa(off)})
		if err != nil {
			t.Fatal(err)
		}
		orig[id] = off
	}
	if _, err := st.Apply(0, []Edit{{Range: rng(0, 2, 0, 5), Text: ""}}); err != nil {
		t.Fatal(err)
	}
	if st.Text() != "abf" {
		t.Fatalf("text=%q", st.Text())
	}
	want := map[int]Range{0: rng(0, 0, 0, 0), 1: rng(0, 1, 0, 1), 2: rng(0, 2, 0, 2), 5: rng(0, 2, 0, 2), 6: rng(0, 3, 0, 3)}
	snap := st.Snapshot()
	gotAlive := map[int]bool{}
	for _, d := range snap.Diagnostics {
		id := int(d.RegisteredAt)
		gotAlive[orig[id]] = true
		if w, ok := want[orig[id]]; ok && d.Range != w {
			t.Errorf("empty orig=%d got %+v want %+v", orig[id], d.Range, w)
		}
	}
	if gotAlive[3] {
		t.Fatal("empty strictly inside deletion must be invalidated")
	}
	if len(snap.Invalidated) != 1 || snap.Invalidated[0].RegisteredAt != int64(idOf(orig, 3)) {
		t.Fatalf("invalidated=%+v", snap.Invalidated)
	}
}

func idOf(m map[int]int, off int) int {
	for id, o := range m {
		if o == off {
			return id
		}
	}
	return -1
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	b := []byte{}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestOverlapInvalidationBoundary(t *testing.T) {
	st := NewStore("abcdef")
	// Delete [2,4). Diagnostics: touching at e, touching at s, overlap by one
	// unit at each side.
	cases := []struct {
		name string
		r    Range
		dead bool
	}{
		{"touch-e", rng(0, 4, 0, 5), false},
		{"touch-s", rng(0, 1, 0, 2), false},
		{"overlap-s", rng(0, 1, 0, 3), true},
		{"overlap-e", rng(0, 3, 0, 5), true},
		{"inside", rng(0, 3, 0, 3), true}, // empty strictly inside
		{"cover", rng(0, 1, 0, 5), true},
	}
	for _, c := range cases {
		if _, err := st.Register(0, Diagnostic{Range: c.r, Message: c.name}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.Apply(0, []Edit{{Range: rng(0, 2, 0, 4), Text: ""}}); err != nil {
		t.Fatal(err)
	}
	alive := map[string]bool{}
	for _, d := range st.Snapshot().Diagnostics {
		alive[d.Message] = true
	}
	for _, c := range cases {
		if alive[c.name] == c.dead {
			t.Errorf("diag %s alive=%v want alive=%v", c.name, alive[c.name], !c.dead)
		}
	}
	gone := st.Snapshot().Invalidated
	if len(gone) != 4 || gone[0].InvalidatedVersion != 1 {
		t.Fatalf("gone=%+v", gone)
	}
}

func TestReplacementWithNewlineShift(t *testing.T) {
	// "aaaa\nbbbb\ncccc" (line length 4 each). Replace "bb" (line1 cols1..3)
	// with "x\ny". Diags wholly after shift across the new line.
	st := NewStore("aaaa\nbbbb\ncccc")
	register := func(name string, r Range) {
		if _, err := st.Register(0, Diagnostic{Range: r, Message: name}); err != nil {
			t.Fatal(err)
		}
	}
	register("before", rng(0, 0, 0, 2))
	register("afterSameLine", rng(1, 3, 2, 1))
	register("laterLine", rng(2, 0, 2, 4))
	if _, err := st.Apply(0, []Edit{{Range: rng(1, 1, 1, 3), Text: "x\ny"}}); err != nil {
		t.Fatal(err)
	}
	if st.Text() != "aaaa\nbx\nyb\ncccc" {
		t.Fatalf("text=%q", st.Text())
	}
	got := map[string]Range{}
	for _, d := range st.Snapshot().Diagnostics {
		got[d.Message] = d.Range
	}
	// Edit: old length 2 units, new length 3 units with one extra line.
	if got["before"] != rng(0, 0, 0, 2) {
		t.Errorf("before=%+v", got["before"])
	}
	// Naive model: aaaa[0-4] b[5] b[6] b[7] b[8] \n[9] cccc[10-13].
	// Edit [6,8) -> "x\ny" (len3, one feed), delta +1.
	//   8->9 = 'y' on line2 col1; old (2,1)=11 -> 12 = line3 col1.
	if got["afterSameLine"] != rng(2, 1, 3, 1) {
		t.Errorf("afterSameLine=%+v", got["afterSameLine"])
	}
	// old (2,0)=10 -> 11 = line3 col0; (2,4)=14 -> 15 = line3 col4.
	if got["laterLine"] != rng(3, 0, 3, 4) {
		t.Errorf("laterLine=%+v", got["laterLine"])
	}
}

func TestDeleteAcrossLinesShift(t *testing.T) {
	st := NewStore("ab\ncd\nef")
	if _, err := st.Register(0, Diagnostic{
		Range: rng(2, 0, 2, 2), Message: "tail",
	}); err != nil {
		t.Fatal(err)
	}
	// Delete from (0,1) to (2,0): removes "b\ncd\n", result "aef".
	if _, err := st.Apply(0, []Edit{{Range: Range{Start: Position{0, 1}, End: Position{2, 0}}, Text: ""}}); err != nil {
		t.Fatal(err)
	}
	if st.Text() != "aef" {
		t.Fatalf("text=%q", st.Text())
	}
	// tail diag old [10,12); delete offsets [1,6) len5 delta -5 -> [5,7),
	// which in "aef" is (0,1)..(0,3).
	d := st.Snapshot().Diagnostics
	if len(d) != 1 || d[0].Range != rng(0, 1, 0, 3) {
		t.Fatalf("diags=%+v", d)
	}
}

func TestDiagnosticOrdering(t *testing.T) {
	st := NewStore("abcdefghij")
	r1 := rng(0, 2, 0, 5)
	r2 := rng(0, 1, 0, 1)
	r3 := rng(0, 2, 0, 3)
	for _, r := range []Range{r1, r2, r3} {
		if _, err := st.Register(0, Diagnostic{Range: r}); err != nil {
			t.Fatal(err)
		}
	}
	d := st.Snapshot().Diagnostics
	want := []Range{r2, r3, r1}
	for i := range want {
		if d[i].Range != want[i] {
			t.Fatalf("order[%d]=%+v want %+v", i, d[i].Range, want[i])
		}
	}
}

func TestInvalidatedOrdering(t *testing.T) {
	st := NewStore("abcdefghij")
	register := func(msg string, r Range) {
		if _, err := st.Register(0, Diagnostic{Range: r, Message: msg}); err != nil {
			t.Fatal(err)
		}
	}
	register("a", rng(0, 0, 0, 2))
	register("b", rng(0, 3, 0, 5))
	if _, err := st.Apply(0, []Edit{{Range: rng(0, 0, 0, 5), Text: ""}}); err != nil {
		t.Fatal(err)
	}
	registerAt(st, t, 1, "c", rng(0, 0, 0, 1))
	if _, err := st.Apply(1, []Edit{{Range: rng(0, 0, 0, 1), Text: ""}}); err != nil {
		t.Fatal(err)
	}
	gone := st.Snapshot().Invalidated
	if len(gone) != 3 {
		t.Fatalf("gone=%+v", gone)
	}
	// Version 1: a registered before b; version 2: c.
	if gone[0].Message != "a" || gone[1].Message != "b" || gone[2].Message != "c" {
		t.Fatalf("order=%s %s %s", gone[0].Message, gone[1].Message, gone[2].Message)
	}
}

func registerAt(st *Store, t *testing.T, v int64, msg string, r Range) {
	t.Helper()
	if _, err := st.Register(v, Diagnostic{Range: r, Message: msg}); err != nil {
		t.Fatalf("register %s: %v", msg, err)
	}
}

func rejectKind(t *testing.T, err error) RejectKind {
	t.Helper()
	re, ok := err.(*RejectError)
	if !ok {
		t.Fatalf("want RejectError got %T %v", err, err)
	}
	return re.Kind
}

func TestRegisterRejectionPrecedence(t *testing.T) {
	st := NewStore("😀x")
	_, _ = st.Apply(0, []Edit{{Range: rng(0, 2, 0, 2), Text: "z"}})
	// Text is now "😀xz" (UTF-16 length 3).
	if _, err := st.Register(0, Diagnostic{Range: rng(0, 0, 0, 9)}); rejectKind(t, err) != RejectStale {
		t.Fatalf("want stale got %v", err)
	}
	if _, err := st.Register(1, Diagnostic{Range: rng(0, 1, 0, 1)}); rejectKind(t, err) != RejectSurrogateSplit {
		t.Fatalf("want surrogate got %v", err)
	}
	if _, err := st.Register(1, Diagnostic{Range: rng(0, 9, 0, 9)}); rejectKind(t, err) != RejectOutOfBounds {
		t.Fatalf("want OOB got %v", err)
	}
}
