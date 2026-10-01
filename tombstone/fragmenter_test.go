package tombstone

import (
	"bytes"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

func b(s string) []byte { return []byte(s) }

func mustRegister(t *testing.T, f *Fragmenter, s, e string, q uint64) {
	t.Helper()
	if err := f.Register(b(s), b(e), q); err != nil {
		t.Fatalf("Register(%q,%q,%d) failed: %v", s, e, q, err)
	}
}

func fragStrings(frags []Fragment) []string {
	out := make([]string, len(frags))
	for i, fr := range frags {
		out[i] = fmt.Sprintf("[%q,%q) seqs=%v", fr.Start, fr.End, fr.Seqs)
	}
	return out
}

// k == s is covered, k == e is not (half-open ranges).
func TestBoundaries(t *testing.T) {
	f := New(8)
	mustRegister(t, f, "b", "d", 5)

	if !f.Covered(b("b"), 1, 10) {
		t.Error("k == s must be covered")
	}
	if f.Covered(b("d"), 1, 10) {
		t.Error("k == e must not be covered")
	}
	if f.Covered(b("a"), 1, 10) {
		t.Error("k < s must not be covered")
	}
	if !f.Covered(b("c"), 1, 10) {
		t.Error("s < k < e must be covered")
	}
}

// q == t does not cover; snap == t is visible; snap just below t is not;
// snap < q is always false.
func TestSeqAndSnapBoundaries(t *testing.T) {
	f := New(8)
	mustRegister(t, f, "a", "z", 7)

	if f.Covered(b("m"), 7, 100) {
		t.Error("q == t must not cover")
	}
	if !f.Covered(b("m"), 1, 7) {
		t.Error("snap == t must be visible")
	}
	if f.Covered(b("m"), 1, 6) {
		t.Error("snap == t-1 must not be visible")
	}
	if f.Covered(b("m"), 9, 8) {
		t.Error("snap < q must be false")
	}
	if f.Covered(b("m"), 9, 9) {
		t.Error("snap == q must be false")
	}
}

// Overlapping tombstones with the same sequence number dedupe to one
// entry in the fragment's sequence set.
func TestSameSeqDedup(t *testing.T) {
	f := New(8)
	mustRegister(t, f, "a", "m", 3)
	mustRegister(t, f, "g", "z", 3)

	// Adjacent fragments share the same set and must merge into one.
	wantMerged := []Fragment{{Start: b("a"), End: b("z"), Seqs: []uint64{3}}}
	got := f.Fragments()
	if !reflect.DeepEqual(got, wantMerged) {
		t.Fatalf("fragments = %v, want %v", fragStrings(got), fragStrings(wantMerged))
	}
}

// Adjacent fragments with identical sets merge; nested tombstones with
// different sets do not.
func TestMergeAndNest(t *testing.T) {
	f := New(8)
	mustRegister(t, f, "a", "c", 1)
	mustRegister(t, f, "c", "e", 1) // adjacent, same set -> merge
	mustRegister(t, f, "g", "p", 2)
	mustRegister(t, f, "j", "m", 5) // nested inside [g,p) -> splits, no merge

	want := []Fragment{
		{Start: b("a"), End: b("e"), Seqs: []uint64{1}},
		{Start: b("g"), End: b("j"), Seqs: []uint64{2}},
		{Start: b("j"), End: b("m"), Seqs: []uint64{5, 2}},
		{Start: b("m"), End: b("p"), Seqs: []uint64{2}},
	}
	got := f.Fragments()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fragments = %v, want %v", fragStrings(got), fragStrings(want))
	}
}

// Rejections report distinguishable reasons in the specified order and
// never change the fragment table or the tombstone count.
func TestRejectedKeepsState(t *testing.T) {
	f := New(2)
	mustRegister(t, f, "a", "c", 1)
	mustRegister(t, f, "e", "g", 2)
	beforeFrags := f.Fragments()
	beforeCount := f.Count()

	check := func(name string, err, want error) {
		t.Helper()
		if err != want {
			t.Errorf("%s: err = %v, want %v", name, err, want)
		}
		if got := f.Count(); got != beforeCount {
			t.Errorf("%s: count changed to %d", name, got)
		}
		if got := f.Fragments(); !reflect.DeepEqual(got, beforeFrags) {
			t.Errorf("%s: fragments changed to %v", name, fragStrings(got))
		}
	}

	// s >= e wins over the other violations.
	check("invalid range", f.Register(b("z"), b("a"), 0), ErrInvalidRange)
	check("equal keys", f.Register(b("a"), b("a"), 1), ErrInvalidRange)
	// q == 0 wins over capacity.
	check("zero seq", f.Register(b("a"), b("b"), 0), ErrZeroSeq)
	// Capacity reached.
	check("cap exceeded", f.Register(b("a"), b("b"), 3), ErrCapExceeded)
	// Non-constructing fragmenter rejects everything.
	dead := New(0)
	check("not constructing", dead.Register(b("a"), b("b"), 1), ErrNotConstructing)
	neg := New(-3)
	check("negative cap", neg.Register(b("a"), b("b"), 1), ErrNotConstructing)
	if dead.Count() != 0 || neg.Count() != 0 {
		t.Error("rejected registrations must not change the count")
	}
}

// The same tombstone set registered in any order yields an identical
// fragment table.
func TestOrderIndependence(t *testing.T) {
	tombstones := []struct {
		s, e string
		q    uint64
	}{
		{"a", "f", 3},
		{"c", "h", 1},
		{"e", "k", 3},
		{"b", "d", 7},
		{"c", "h", 1}, // exact duplicate still counts
	}
	var tables [][]Fragment
	perm := rand.New(rand.NewSource(42))
	for trial := 0; trial < 12; trial++ {
		f := New(16)
		order := perm.Perm(len(tombstones))
		for _, i := range order {
			ts := tombstones[i]
			mustRegister(t, f, ts.s, ts.e, ts.q)
		}
		tables = append(tables, f.Fragments())
	}
	for i := 1; i < len(tables); i++ {
		if !reflect.DeepEqual(tables[0], tables[i]) {
			t.Fatalf("order %d differs:\n%v\nvs\n%v",
				i, fragStrings(tables[0]), fragStrings(tables[i]))
		}
	}
}

// Concurrent registration, queries and lookups behave as some serial
// order (run with -race).
func TestConcurrent(t *testing.T) {
	const n = 64
	f := New(n)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := g; i < n; i += 8 {
				s := fmt.Sprintf("%04d", i*2)
				e := fmt.Sprintf("%04d", i*2+2)
				_ = f.Register(b(s), b(e), uint64(i+1))
			}
		}(g)
	}
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_ = f.Covered(b("0064"), 1, uint64(i))
				_ = f.Fragments()
				_ = f.Count()
			}
		}()
	}
	wg.Wait()
	if got := f.Count(); got != n {
		t.Fatalf("count = %d, want %d", got, n)
	}
}

// Covered examines O(log n) fragments via binary search, proven by the
// non-exported counter at 1k vs 100k fragments.
func TestExaminedScaling(t *testing.T) {
	build := func(n int) *Fragmenter {
		f := New(n)
		for i := 0; i < n; i++ {
			s := fmt.Sprintf("%08d", i*2)
			e := fmt.Sprintf("%08d", i*2+1)
			mustRegister(t, f, s, e, uint64(i+1))
		}
		if got := len(f.Fragments()); got != n {
			t.Fatalf("fragments = %d, want %d", got, n)
		}
		return f
	}
	probe := func(f *Fragmenter, k string) int64 {
		f.ResetExamined()
		f.Covered(b(k), 0, ^uint64(0))
		return f.Examined()
	}

	small := build(1000)
	large := build(100000)
	eSmall := probe(small, "00000999")
	eLarge := probe(large, "00099999")
	t.Logf("fragments=1000 examined=%d; fragments=100000 examined=%d", eSmall, eLarge)

	// Binary search over n fragments examines about log2(n)+1 of them.
	if eSmall > 32 {
		t.Errorf("1000 fragments: examined %d, want <= 32", eSmall)
	}
	if eLarge > 64 {
		t.Errorf("100000 fragments: examined %d, want <= 64", eLarge)
	}
	if eLarge > eSmall*2 {
		t.Errorf("examined grows faster than logarithmic: %d -> %d", eSmall, eLarge)
	}
}

type naiveTombstone struct {
	s, e []byte
	q    uint64
}

func naiveCovered(ts []naiveTombstone, k []byte, q, snap uint64) (bool, uint64) {
	for _, t := range ts {
		if bytes.Compare(t.s, k) <= 0 && bytes.Compare(k, t.e) < 0 &&
			q < t.q && t.q <= snap {
			return true, t.q
		}
	}
	return false, 0
}

// 2000 random inputs cross-checked against the naive per-tombstone
// scan, logging input, output and the deciding evidence.
func TestNaiveCompare(t *testing.T) {
	rng := rand.New(rand.NewSource(1010))
	const groups = 2000
	mismatches := 0
	for g := 0; g < groups; g++ {
		n := 1 + rng.Intn(6)
		f := New(n)
		ts := make([]naiveTombstone, 0, n)
		for i := 0; i < n; i++ {
			lo := rng.Intn(90)
			hi := lo + 1 + rng.Intn(90-lo)
			s := fmt.Sprintf("%02d", lo)
			e := fmt.Sprintf("%02d", hi)
			q := uint64(1 + rng.Intn(10))
			mustRegister(t, f, s, e, q)
			ts = append(ts, naiveTombstone{s: b(s), e: b(e), q: q})
		}
		k := fmt.Sprintf("%02d", rng.Intn(100))
		q := uint64(rng.Intn(12))
		snap := uint64(rng.Intn(12))

		got := f.Covered(b(k), q, snap)
		want, by := naiveCovered(ts, b(k), q, snap)
		t.Logf("group=%d tombstones=%v k=%q q=%d snap=%d got=%v want=%v decidedBySeq=%d",
			g, ts, k, q, snap, got, want, by)
		if got != want {
			mismatches++
			t.Errorf("group %d: Covered(%q,%d,%d) = %v, naive = %v (tombstones %v)",
				g, k, q, snap, got, want, ts)
		}
	}
	if mismatches > 0 {
		t.Fatalf("%d/%d groups mismatched the naive scan", mismatches, groups)
	}
}
