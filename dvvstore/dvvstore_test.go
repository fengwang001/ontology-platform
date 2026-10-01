package dvvstore

import (
	"errors"
	"reflect"
	"testing"
)

func mustNew(t *testing.T, cap int) *Store {
	t.Helper()
	s, err := New(cap)
	if err != nil {
		t.Fatalf("New(%d) unexpected error: %v", cap, err)
	}
	return s
}

func snapDots(snap Snapshot) []Dot {
	out := make([]Dot, len(snap.Siblings))
	for i, sib := range snap.Siblings {
		out[i] = sib.Dot
	}
	return out
}

func snapValues(snap Snapshot) []string {
	out := make([]string, len(snap.Siblings))
	for i, sib := range snap.Siblings {
		out[i] = sib.Value
	}
	return out
}

// TestNewRejectsSmallCap covers the constructor rule.
func TestNewRejectsSmallCap(t *testing.T) {
	for _, cap := range []int{0, -1, -42} {
		if _, err := New(cap); !errors.Is(err, ErrInvalidCap) {
			t.Fatalf("New(%d) err = %v, want ErrInvalidCap", cap, err)
		}
	}
	t.Logf("input: New(cap<1); output: ErrInvalidCap; reason: cap must be at least 1")
}

// TestPutValidationPriority checks that errors follow the documented order
// and that rejected Puts never mutate state.
func TestPutValidationPriority(t *testing.T) {
	s := mustNew(t, 1)
	if err := s.Put("k", "A", nil, "v1"); err != nil {
		t.Fatal(err)
	}
	before := s.Get("k")

	cases := []struct {
		name string
		key  string
		id   string
		ctx  map[string]int64
		want error
	}{
		{"empty key first", "", "A", nil, ErrEmptyKey},
		{"empty id second", "k", "", nil, ErrEmptyNodeID},
		{"empty ctx node third", "k", "A", map[string]int64{"": 1}, ErrEmptyContextNode},
		{"ctx ahead fourth", "k", "A", map[string]int64{"A": 9}, ErrContextAhead},
		{"ctx ahead unknown node", "k", "A", map[string]int64{"Z": 1}, ErrContextAhead},
		{"cap fifth", "k", "B", map[string]int64{"A": 0}, ErrCapExceeded},
	}
	for _, tc := range cases {
		err := s.Put(tc.key, tc.id, tc.ctx, "v")
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
		if got := s.Get("k"); !reflect.DeepEqual(got, before) {
			t.Fatalf("%s: rejected Put mutated state: %#v", tc.name, got)
		}
		t.Logf("input: Put(%q,%q,%v); output: %v; judgment: documented priority", tc.key, tc.id, tc.ctx, err)
	}
}

// TestContextCoversEqualAndMinusOne checks ctx[j] == m deletes the sibling
// while ctx[j] == m-1 keeps it as a concurrent sibling.
func TestContextCoversEqualAndMinusOne(t *testing.T) {
	equal := mustNew(t, 4)
	if err := equal.Put("k", "B", nil, "b1"); err != nil {
		t.Fatal(err)
	}
	if err := equal.Put("k", "A", map[string]int64{"B": 1}, "a1"); err != nil {
		t.Fatal(err)
	}
	snap := equal.Get("k")
	if got, want := snapDots(snap), []Dot{{ID: "A", N: 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ctx[j]==m: dots = %v, want %v", got, want)
	}
	t.Logf("input: B empty-ctx, then A ctx{B:1}; output dots: %v; judgment: ctx[B]>=1 covers (B,1)", snapDots(snap))

	minusOne := mustNew(t, 4)
	if err := minusOne.Put("k", "B", nil, "b1"); err != nil {
		t.Fatal(err)
	}
	if err := minusOne.Put("k", "A", map[string]int64{"B": 0}, "a1"); err != nil {
		t.Fatal(err)
	}
	snap = minusOne.Get("k")
	if got, want := snapDots(snap), []Dot{{ID: "A", N: 1}, {ID: "B", N: 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ctx[j]==m-1: dots = %v, want %v", got, want)
	}
	if got, want := snapValues(snap), []string{"a1", "b1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sorted values = %v, want %v", got, want)
	}
	t.Logf("input: B empty-ctx, then A ctx{B:0}; output dots: %v; judgment: ctx[B]=0 < 1, (B,1) survives", snapDots(snap))
}

// TestEmptyContextAdvancesCounter checks the new counter is seen[id]+1, so
// a second empty-context write by A yields (A,2); a later full-context
// write covers every sibling.
func TestEmptyContextAdvancesCounterAndFullCover(t *testing.T) {
	s := mustNew(t, 4)
	if err := s.Put("k", "A", nil, "a1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Put("k", "A", map[string]int64{}, "a2"); err != nil {
		t.Fatal(err)
	}
	snap := s.Get("k")
	if got, want := snapDots(snap), []Dot{{ID: "A", N: 1}, {ID: "A", N: 2}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("dots = %v, want %v", got, want)
	}
	t.Logf("input: A empty-ctx twice; output dots: %v; judgment: counter is seen[A]+1", snapDots(snap))

	// Old context: A has only seen up to 0, so this is a concurrent sibling.
	if err := s.Put("k", "B", map[string]int64{"A": 0}, "b1"); err != nil {
		t.Fatal(err)
	}
	// Full context covers (A,1),(A,2),(B,1), new dot is (A,3).
	if err := s.Put("k", "A", map[string]int64{"A": 2, "B": 1}, "a3"); err != nil {
		t.Fatal(err)
	}
	snap = s.Get("k")
	if got, want := snapDots(snap), []Dot{{ID: "A", N: 3}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("full-context dots = %v, want %v", got, want)
	}
	if got, want := snap.Context, map[string]int64{"A": 3, "B": 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("context = %v, want %v", got, want)
	}
	t.Logf("input: A ctx{A:2,B:1}; output dots: %v; judgment: every old dot has ctx[j]>=m", snapDots(snap))
}

// TestCapJudgedAfterRemoval: cap=1, a full-context write that removes both
// survivors succeeds while an empty-context write is rejected.
func TestCapJudgedAfterRemoval(t *testing.T) {
	s := mustNew(t, 1)
	if err := s.Put("k", "A", nil, "a1"); err != nil {
		t.Fatal(err)
	}
	before := s.Get("k")

	// Empty ctx covers nothing: the old (A,1) survives, so 1+1 > cap 1.
	err := s.Put("k", "A", nil, "a2")
	if !errors.Is(err, ErrCapExceeded) {
		t.Fatalf("empty-ctx Put err = %v, want ErrCapExceeded", err)
	}
	if got := s.Get("k"); !reflect.DeepEqual(got, before) {
		t.Fatalf("cap rejection mutated state: %#v", got)
	}
	t.Logf("input: empty-ctx A, 1 survivor, cap=1; output: ErrCapExceeded; judgment: 1+1 > 1 after zero removals")

	// Full context removes (A,1) first, leaving room for the new dot.
	if err := s.Put("k", "A", map[string]int64{"A": 1}, "a3"); err != nil {
		t.Fatalf("full-ctx Put err = %v, want nil", err)
	}
	snap := s.Get("k")
	if got, want := snapDots(snap), []Dot{{ID: "A", N: 2}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("dots = %v, want %v", got, want)
	}
	t.Logf("input: A ctx{A:1} cap=1; output dots: %v; judgment: survivor covered, 0+1 <= 1", snapDots(snap))
}

// TestGetMissingKeyEmptyValueAndNoAlias covers missing-key semantics,
// empty-string values, and detached return values.
func TestGetMissingKeyEmptyValueAndNoAlias(t *testing.T) {
	s := mustNew(t, 2)
	snap := s.Get("missing")
	if len(snap.Siblings) != 0 || len(snap.Context) != 0 {
		t.Fatalf("missing key = %#v, want empty", snap)
	}
	if err := s.Put("k", "A", nil, ""); err != nil {
		t.Fatal(err)
	}
	snap = s.Get("k")
	if snap.Siblings[0].Value != "" {
		t.Fatalf("empty value = %q, want \"\"", snap.Siblings[0].Value)
	}

	snap.Siblings[0].Value = "x"
	snap.Context["A"] = 99
	again := s.Get("k")
	if again.Siblings[0].Value != "" || again.Context["A"] != 1 {
		t.Fatalf("returned state aliases internal storage: %#v", again)
	}
	t.Logf("input: Get(missing), Put(\"\"), mutate returned maps; output: store unchanged; judgment: detached copies")
}

// TestMergeRules covers same-dot value resolution, unseen survival,
// deletion when the other side has covered the dot, one-sided key copy
// (ignoring cap), and self-merge.
func TestMergeRules(t *testing.T) {
	left := mustNew(t, 4)
	right := mustNew(t, 4)
	if err := left.Put("k", "A", nil, "L"); err != nil {
		t.Fatal(err)
	}
	if err := right.Put("k", "A", nil, "R"); err != nil {
		t.Fatal(err)
	}
	// ctx{A:0} keeps (A,1) concurrent with the fresh (B,1) on right.
	if err := right.Put("k", "B", map[string]int64{"A": 0}, "b1"); err != nil {
		t.Fatal(err)
	}

	left.Merge(right)
	snap := left.Get("k")
	if got, want := snapDots(snap), []Dot{{ID: "A", N: 1}, {ID: "B", N: 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("merged dots = %v, want %v", got, want)
	}
	if snap.Siblings[0].Value != "R" {
		t.Fatalf("same-dot value = %q, want lexicographically larger \"R\"", snap.Siblings[0].Value)
	}
	if got, want := snap.Context, map[string]int64{"A": 1, "B": 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("merged context = %v, want %v", got, want)
	}
	t.Logf("input: merge same-dot L/R + B unseen; output dots: %v; judgment: dedup max-value, left seen[B]=0<1 keeps B", snapDots(snap))

	// advanced has already merged left, then C wrote with full context:
	// it has seen A:1,B:1 but only holds (C,1). Merging left again must not
	// resurrect the covered dots.
	advanced := mustNew(t, 4)
	advanced.Merge(left)
	if err := advanced.Put("k", "C", map[string]int64{"A": 1, "B": 1}, "c1"); err != nil {
		t.Fatal(err)
	}
	before := advanced.Get("k")
	advanced.Merge(left)
	snap = advanced.Get("k")
	if got, want := snapDots(snap), []Dot{{ID: "C", N: 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("covered dots = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(snap, before) {
		t.Fatalf("merge of fully-covered dots mutated state: %#v", snap)
	}
	t.Logf("input: merge left into replica that saw A:1,B:1; output dots: %v; judgment: no same dot, seen[j]>=m deletes", snapDots(snap))

	oneSided := mustNew(t, 1)
	oneSided.Merge(right)
	if got, want := oneSided.Get("k"), right.Get("k"); !reflect.DeepEqual(got, want) {
		t.Fatalf("one-sided key = %#v, want %#v", got, want)
	}
	t.Logf("input: merge key absent locally with cap=1; output: whole key copied; judgment: Merge ignores cap")

	selfStore := mustNew(t, 4)
	if err := selfStore.Put("k", "A", nil, "a1"); err != nil {
		t.Fatal(err)
	}
	selfBefore := selfStore.Get("k")
	selfStore.Merge(selfStore)
	if got := selfStore.Get("k"); !reflect.DeepEqual(got, selfBefore) {
		t.Fatalf("self-merge mutated state: %#v", got)
	}
	t.Logf("input: Merge(self); output: identical state; judgment: snapshot makes self-merge a no-op")
}

// TestMergePrunesLocalSiblingsWhenOtherOnlyHasSeen covers a key present on
// both sides where the other side's sibling list is empty but its seen
// vector has covered local dots: the local siblings must still be pruned.
func TestMergePrunesLocalSiblingsWhenOtherOnlyHasSeen(t *testing.T) {
	a := mustNew(t, 4)
	b := mustNew(t, 4)

	if err := a.Put("k", "A", nil, "a1"); err != nil {
		t.Fatal(err)
	}
	b.Merge(a)
	// B covers (A,1) and replaces it with (B,1); B still holds seen A:1.
	if err := b.Put("k", "B", map[string]int64{"A": 1}, "b1"); err != nil {
		t.Fatal(err)
	}
	if got, want := snapDots(b.Get("k")), []Dot{{ID: "B", N: 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("setup dots = %v, want %v", got, want)
	}
	// Merge B into A: B has seen A:1 but no (A,1) sibling, so A's (A,1)
	// must still be deleted; (B,1) is unseen by A and survives.
	a.Merge(b)
	snap := a.Get("k")
	if got, want := snapDots(snap), []Dot{{ID: "B", N: 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("dots = %v, want %v (local (A,1) pruned by other side's seen only)", got, want)
	}
	if got, want := snap.Context, map[string]int64{"A": 1, "B": 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("context = %v, want %v", got, want)
	}
	t.Logf("input: A{(A,1)} merge B{(B,1), seen A:1}; output dots: %v; judgment: union of key sets, local dot pruned by other side seen alone", snapDots(snap))
}
