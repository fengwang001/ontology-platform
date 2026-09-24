package norm

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"ontology/rfold"
	"reflect"
	"sync"
	"testing"
)

// oracle independently counts the minimal diff from full pre/post snapshots.
func oracle(pre map[string]int64, b []rfold.Op) int {
	post := maps.Clone(pre)
	naiveApply(post, b)
	w := 0
	for k, pv := range pre {
		if av, ok := post[k]; !ok {
			w++
		} else if av != pv {
			w += 2
		}
	}
	for k := range post {
		if _, ok := pre[k]; !ok {
			w++
		}
	}
	return w
}

// randomChecks feeds randomized batch streams, asserting I1 (replay == naive
// == snapshot), I2 (every prefix valid) and I3 (minimal count) per seed.
func randomChecks(t *testing.T, start int64, n int) {
	t.Helper()
	for s := start; s < start+int64(n); s++ {
		r := rand.New(rand.NewSource(s))
		nz := New(1 << 30)
		ref := map[string]int64{}
		for round := 0; round < 50; round++ {
			pre := nz.Snapshot()
			b := []rfold.Op{}
			for j := r.Intn(5); j >= 0; j-- {
				k := fmt.Sprintf("k%d", r.Intn(8))
				if r.Intn(4) == 0 {
					b = append(b, dl(k))
				} else {
					b = append(b, up(k, r.Int63n(16)-8))
				}
			}
			got, err := nz.Apply(b)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != oracle(pre, b) {
				t.Fatalf("I3 seed %d round %d", s, round)
			}
			naiveApply(ref, b)
		}
		tbl, ok := replayChk(nz.Log())
		if !ok || !reflect.DeepEqual(tbl, ref) || !reflect.DeepEqual(nz.Snapshot(), ref) {
			t.Fatalf("I1/I2 seed %d", s)
		}
	}
}

func TestReferenceEquivalence(t *testing.T) { randomChecks(t, 0, 12) }
func TestLogPrefixesValid(t *testing.T)     { randomChecks(t, 100, 12) }

func TestMinimality(t *testing.T) {
	nz := New(10)
	for i, tc := range []struct {
		b []rfold.Op
		w []rfold.Change
	}{
		{[]rfold.Op{up("a", 1)}, []rfold.Change{ins("a", 1)}},
		{[]rfold.Op{up("a", 1)}, nil},
		{[]rfold.Op{up("a", 2)}, []rfold.Change{ret("a", 1), ins("a", 2)}},
		{[]rfold.Op{dl("a")}, []rfold.Change{ret("a", 2)}},
		{[]rfold.Op{dl("a")}, nil},
		{[]rfold.Op{up("b", 1), up("b", 1)}, []rfold.Change{ins("b", 1)}},
		{[]rfold.Op{dl("b"), up("b", 1)}, nil},
		{[]rfold.Op{up("c", 1), dl("c")}, nil},
	} {
		got, err := nz.Apply(tc.b)
		if err != nil || !reflect.DeepEqual(got, tc.w) {
			t.Fatalf("case %d: %v %v", i, err, got)
		}
	}
}

func TestRejectionAtomic(t *testing.T) {
	nz := New(2)
	if _, err := nz.Apply([]rfold.Op{up("a", 1), up("b", 2)}); err != nil {
		t.Fatal(err)
	}
	snap, n0 := nz.Snapshot(), len(nz.Log())
	for i, tc := range []struct {
		b    []rfold.Op
		want error
	}{
		{[]rfold.Op{up("", 1)}, rfold.ErrEmptyKey},
		{[]rfold.Op{{Kind: rfold.OpKind(9), Key: "x"}}, rfold.ErrInvalidOp},
		{[]rfold.Op{up("c", 3)}, ErrTooManyKeys},
		{[]rfold.Op{up("c", 3), up("", 1)}, rfold.ErrEmptyKey},
	} {
		got, err := nz.Apply(tc.b)
		if !errors.Is(err, tc.want) || got != nil ||
			!reflect.DeepEqual(nz.Snapshot(), snap) || len(nz.Log()) != n0 {
			t.Fatalf("case %d: %v", i, err)
		}
	}
	if rfold.ErrEmptyKey == rfold.ErrInvalidOp || rfold.ErrInvalidOp == ErrTooManyKeys ||
		rfold.ErrEmptyKey == ErrTooManyKeys {
		t.Fatal("sentinel errors must be distinct")
	}
	if _, err := nz.Apply([]rfold.Op{up("a", 9)}); err != nil {
		t.Fatalf("unusable after rejection: %v", err)
	}
}

func TestComplexityCounter(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		nz := New(1 << 20)
		big := make([]rfold.Op, m)
		for i := range big {
			big[i] = up(fmt.Sprintf("k%05d", i), 1)
		}
		if _, err := nz.Apply(big); err != nil {
			t.Fatal(err)
		}
		if _, err := nz.Apply([]rfold.Op{up("k00000", 2)}); err != nil {
			t.Fatal(err)
		}
		if c := lastCheckedOf(nz); c > 3 {
			t.Fatalf("m=%d single-key checked %d", m, c)
		}
		for _, tc := range []int{1, 5, 50} {
			b := make([]rfold.Op, tc)
			for j := range b {
				b[j] = up(fmt.Sprintf("t%d-%d", m, j), 1)
			}
			if _, err := nz.Apply(b); err != nil {
				t.Fatal(err)
			}
			if c := lastCheckedOf(nz); c > tc+2 {
				t.Fatalf("m=%d t=%d checked %d", m, tc, c)
			}
		}
	}
}

func TestConcurrentApply(t *testing.T) {
	const G, R = 16, 100
	nz := New(1 << 20)
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			k := fmt.Sprintf("g%d", g)
			for j := 0; j < R; j++ {
				if _, err := nz.Apply([]rfold.Op{up(k, int64(2*j)), up(k, int64(2*j+1))}); err != nil {
					t.Errorf("apply: %v", err)
				}
			}
		}(g)
	}
	wg.Wait()
	lg := nz.Log()
	tbl, ok := replayChk(lg)
	if !ok || !reflect.DeepEqual(tbl, nz.Snapshot()) {
		t.Fatal("folded log invalid under concurrency")
	}
	// Each batch j>=1 of goroutine g emits exactly -(2j-1) +(2j+1); it is
	// contiguous iff that adjacent pair appears in the global log.
	seen := map[string]int{}
	for i := 0; i+1 < len(lg); i++ {
		if lg[i].Kind == rfold.ChgRetract && lg[i+1].Kind == rfold.ChgInsert &&
			lg[i+1].Key == lg[i].Key && lg[i+1].Val == lg[i].Val+2 {
			seen[lg[i].Key]++
		}
	}
	if len(seen) != G {
		t.Fatalf("want %d contiguous goroutines, got %d", G, len(seen))
	}
	for g := 0; g < G; g++ {
		if got := seen[fmt.Sprintf("g%d", g)]; got != R-1 {
			t.Fatalf("g%d: %d contiguous pairs, want %d", g, got, R-1)
		}
	}
}
