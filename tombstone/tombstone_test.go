package tombstone

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

func key(b byte) []byte { return []byte{b} }

func mustRegister(t *testing.T, f *Fragmenter, s, e []byte, q uint64) {
	t.Helper()
	if err := f.Register(s, e, q); err != nil {
		t.Fatalf("Register(%v,%v,%d) failed: %v", s, e, q, err)
	}
}

func naiveCovered(tss []Tombstone, k []byte, q, snap uint64) (bool, uint64) {
	for _, ts := range tss {
		if bytes.Compare(ts.Start, k) <= 0 && bytes.Compare(k, ts.End) < 0 &&
			q < ts.Seq && ts.Seq <= snap {
			return true, ts.Seq
		}
	}
	return false, 0
}

func TestBoundaryKeys(t *testing.T) {
	f := New(4)
	mustRegister(t, f, key(10), key(20), 5)
	if !f.Covered(key(10), 1, 9) {
		t.Error("k == s must be covered (range is left-closed)")
	}
	if f.Covered(key(20), 1, 9) {
		t.Error("k == e must not be covered (range is right-open)")
	}
	if f.Covered(key(9), 1, 9) || f.Covered(key(21), 1, 9) {
		t.Error("keys outside [s,e) must not be covered")
	}
}

func TestSeqAndSnapBoundaries(t *testing.T) {
	f := New(4)
	mustRegister(t, f, key(10), key(20), 5)
	if f.Covered(key(15), 5, 9) {
		t.Error("q == t must not cover (needs q < t)")
	}
	if !f.Covered(key(15), 4, 5) {
		t.Error("snap == t must be visible (needs t <= snap)")
	}
	if f.Covered(key(15), 4, 4) {
		t.Error("snap == t-1 must not be visible")
	}
	if f.Covered(key(15), 9, 4) {
		t.Error("snap < q must always be false")
	}
}

func TestSameSeqOverlapDedup(t *testing.T) {
	f := New(4)
	mustRegister(t, f, key(10), key(30), 5)
	mustRegister(t, f, key(20), key(40), 5)
	frags := f.Fragments()
	if len(frags) != 1 {
		t.Fatalf("expected 1 merged fragment, got %d: %+v", len(frags), frags)
	}
	if !reflect.DeepEqual(frags[0].Seqs, []uint64{5}) {
		t.Errorf("same-seq overlap must dedup to [5], got %v", frags[0].Seqs)
	}
	if !bytes.Equal(frags[0].Start, key(10)) || !bytes.Equal(frags[0].End, key(40)) {
		t.Errorf("expected merged range [10,40), got [%v,%v)", frags[0].Start, frags[0].End)
	}
}

func TestAdjacentSameSetMerges(t *testing.T) {
	f := New(4)
	mustRegister(t, f, key(10), key(20), 1)
	mustRegister(t, f, key(20), key(30), 1)
	frags := f.Fragments()
	if len(frags) != 1 {
		t.Fatalf("adjacent equal sets must merge into 1 fragment, got %d", len(frags))
	}
	if !bytes.Equal(frags[0].Start, key(10)) || !bytes.Equal(frags[0].End, key(30)) {
		t.Errorf("expected [10,30), got [%v,%v)", frags[0].Start, frags[0].End)
	}
}

func TestNestedDifferentSetsDoNotMerge(t *testing.T) {
	f := New(4)
	mustRegister(t, f, key(10), key(40), 1)
	mustRegister(t, f, key(20), key(30), 2)
	frags := f.Fragments()
	want := []Fragment{
		{Start: key(10), End: key(20), Seqs: []uint64{1}},
		{Start: key(20), End: key(30), Seqs: []uint64{2, 1}},
		{Start: key(30), End: key(40), Seqs: []uint64{1}},
	}
	if !reflect.DeepEqual(frags, want) {
		t.Errorf("nested fragments mismatch:\n got %+v\nwant %+v", frags, want)
	}
}

func TestRejectionsDoNotChangeState(t *testing.T) {
	f := New(1)
	mustRegister(t, f, key(10), key(20), 5)
	before := f.Fragments()
	beforeCount := f.Count()

	cases := []struct {
		name string
		s, e []byte
		q    uint64
		want error
	}{
		{"range", key(20), key(10), 1, ErrInvalidRange},
		{"empty-range", key(15), key(15), 1, ErrInvalidRange},
		{"zero-seq", key(1), key(2), 0, ErrZeroSeq},
		{"cap", key(1), key(2), 1, ErrCapExceeded},
	}
	for _, tc := range cases {
		if err := f.Register(tc.s, tc.e, tc.q); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
	if got := f.Count(); got != beforeCount {
		t.Errorf("count changed after rejections: %d -> %d", beforeCount, got)
	}
	if got := f.Fragments(); !reflect.DeepEqual(got, before) {
		t.Errorf("fragments changed after rejections:\n got %+v\nwant %+v", got, before)
	}

	dead := New(0)
	if err := dead.Register(key(1), key(2), 1); !errors.Is(err, ErrInvalidCap) {
		t.Errorf("non-positive cap: got %v, want %v", err, ErrInvalidCap)
	}
	if err := dead.Register(key(2), key(1), 0); !errors.Is(err, ErrInvalidRange) {
		t.Errorf("rejection order: invalid range must win, got %v", err)
	}
	if err := dead.Register(key(1), key(2), 0); !errors.Is(err, ErrZeroSeq) {
		t.Errorf("rejection order: zero seq must beat cap, got %v", err)
	}
}

func TestOrderIndependence(t *testing.T) {
	tss := []Tombstone{
		{Start: key(1), End: key(9), Seq: 3},
		{Start: key(2), End: key(5), Seq: 7},
		{Start: key(4), End: key(8), Seq: 7},
		{Start: key(3), End: key(6), Seq: 1},
		{Start: key(6), End: key(9), Seq: 2},
		{Start: key(2), End: key(5), Seq: 7},
	}
	rng := rand.New(rand.NewSource(42))
	var reference []Fragment
	for trial := 0; trial < 8; trial++ {
		perm := rng.Perm(len(tss))
		f := New(len(tss))
		for _, i := range perm {
			mustRegister(t, f, tss[i].Start, tss[i].End, tss[i].Seq)
		}
		got := f.Fragments()
		if trial == 0 {
			reference = got
			continue
		}
		if !reflect.DeepEqual(got, reference) {
			t.Fatalf("trial %d (perm %v): fragment table differs:\n got %+v\nwant %+v",
				trial, perm, got, reference)
		}
	}
}

func bigKey(i uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], i)
	return b[:]
}

func buildDisjoint(t *testing.T, n int) *Fragmenter {
	t.Helper()
	f := New(n)
	for i := 0; i < n; i++ {
		mustRegister(t, f, bigKey(uint64(2*i)), bigKey(uint64(2*i+1)), uint64(i+1))
	}
	if got := len(f.Fragments()); got != n {
		t.Fatalf("expected %d fragments, got %d", n, got)
	}
	return f
}

func TestCoveredExaminedDoesNotGrowLinearly(t *testing.T) {
	small := buildDisjoint(t, 1000)
	large := buildDisjoint(t, 100000)

	small.examined.Store(0)
	if !small.Covered(bigKey(998), 0, 1<<60) {
		t.Fatal("expected covered in 1000-fragment table")
	}
	smallExamined := small.examined.Load()

	large.examined.Store(0)
	if !large.Covered(bigKey(99998), 0, 1<<60) {
		t.Fatal("expected covered in 100000-fragment table")
	}
	largeExamined := large.examined.Load()

	t.Logf("fragments=1000 examined=%d; fragments=100000 examined=%d (100x fragments, %.2fx examined)",
		smallExamined, largeExamined, float64(largeExamined)/float64(smallExamined))
	if largeExamined >= smallExamined*4 {
		t.Errorf("examined count grows too fast: %d -> %d", smallExamined, largeExamined)
	}
}

func TestRandomVsNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	for trial := 0; trial < 2000; trial++ {
		n := 1 + rng.Intn(8)
		f := New(n)
		tss := make([]Tombstone, 0, n)
		for i := 0; i < n; i++ {
			s := byte(rng.Intn(10))
			e := s + 1 + byte(rng.Intn(int(10-s)))
			q := uint64(1 + rng.Intn(6))
			mustRegister(t, f, key(s), key(e), q)
			tss = append(tss, Tombstone{Start: key(s), End: key(e), Seq: q})
		}
		k := key(byte(rng.Intn(11)))
		q := uint64(rng.Intn(8))
		snap := uint64(rng.Intn(8))
		got := f.Covered(k, q, snap)
		want, witness := naiveCovered(tss, k, q, snap)
		basis := fmt.Sprintf("no tombstone seq in (%d, %d] covers k=%v", q, snap, k)
		if want {
			basis = fmt.Sprintf("tombstone seq t=%d covers k=%v and %d < %d <= %d", witness, k, q, witness, snap)
		}
		t.Logf("trial=%d tombstones=%+v k=%v q=%d snap=%d got=%v want=%v basis=%s",
			trial, tss, k, q, snap, got, want, basis)
		if got != want {
			t.Errorf("trial %d: got %v, want %v (%s)", trial, got, want, basis)
		}
	}
}

func TestConcurrentAccess(t *testing.T) {
	const writers = 8
	const perWriter = 50
	f := New(writers * perWriter)
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				s := bigKey(uint64(2 * (w*perWriter + i)))
				e := bigKey(uint64(2*(w*perWriter+i) + 1))
				if err := f.Register(s, e, uint64(i+1)); err != nil {
					t.Errorf("register: %v", err)
					return
				}
			}
		}(w)
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				f.Covered(bigKey(uint64(i)), uint64(i%7), uint64(i%11))
				f.Fragments()
				f.Count()
			}
		}()
	}
	wg.Wait()
	if got := f.Count(); got != writers*perWriter {
		t.Errorf("count = %d, want %d", got, writers*perWriter)
	}
	if got := len(f.Fragments()); got != writers*perWriter {
		t.Errorf("fragments = %d, want %d", got, writers*perWriter)
	}
}
