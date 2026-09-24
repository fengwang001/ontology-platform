package rank

import (
	"errors"
	"testing"

	"ontology/key"
	"ontology/list"
)

func buildList(t *testing.T, maxLevel, maxElems int, keys []key.K) *list.List {
	t.Helper()
	l, err := list.New(maxLevel, maxElems)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if err := l.Insert(k); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

func TestVisitedNodesSublinear(t *testing.T) {
	for _, n := range []int{1000, 100000} {
		l, _ := list.New(key.MaxLevel, n)
		for k := 0; k < n; k++ {
			if err := l.Insert(key.K(k)); err != nil {
				t.Fatal(err)
			}
		}
		q := New(l)
		if _, err := q.At(n / 2); err != nil {
			t.Fatal(err)
		}
		atHops := q.visited.Load()
		q.RankOf(key.K(n / 2))
		rankHops := q.visited.Load()
		if atHops > 80 || rankHops > 80 {
			t.Fatalf("n=%d: visited At=%d RankOf=%d, want <=80", n, atHops, rankHops)
		}
		t.Logf("n=%d visited At=%d RankOf=%d", n, atHops, rankHops)
	}
}

func TestRankConsistency(t *testing.T) {
	keys := []key.K{3, 1, 4, 1, 5, 9, 2, 6, 5, 3, 5}
	q := New(buildList(t, key.MaxLevel, 100, keys))
	for _, tc := range []struct {
		lo, hi key.K
		want   int
	}{
		{1, 9, 11}, {3, 5, 6}, {5, 5, 3}, {0, 0, 0}, {7, 8, 0}, {10, 20, 0}, {0, 100, 11},
	} {
		got, err := q.Range(tc.lo, tc.hi)
		if err != nil {
			t.Fatal(err)
		}
		if diff := q.RankOf(tc.hi+1) - q.RankOf(tc.lo); got != tc.want || diff != tc.want {
			t.Fatalf("Range(%d,%d)=%d diff=%d, want %d", tc.lo, tc.hi, got, diff, tc.want)
		}
	}
	for i := 0; i < len(keys); i++ {
		v, err := q.At(i)
		if err != nil || q.RankOf(v) > i {
			t.Fatalf("At(%d)=%v err=%v not inverse of RankOf", i, v, err)
		}
		if w, _ := q.At(q.RankOf(v)); w != v {
			t.Fatalf("At(RankOf(%d))!=%d", v, v)
		}
	}
}

func TestRankErrors(t *testing.T) {
	q := New(buildList(t, key.MaxLevel, 100, []key.K{1, 2, 3}))
	for _, bad := range []int{-1, 3, 100} {
		if _, err := q.At(bad); !errors.Is(err, list.ErrOutOfRange) {
			t.Fatalf("At(%d) err=%v, want ErrOutOfRange", bad, err)
		}
	}
	if _, err := q.Range(3, 2); !errors.Is(err, ErrBadRange) {
		t.Fatalf("Range(3,2) err=%v, want ErrBadRange", err)
	}
	if got := q.RankOf(0); got != 0 {
		t.Fatalf("RankOf(absent 0)=%d, want 0", got)
	}
	if got := q.RankOf(99); got != 3 {
		t.Fatalf("RankOf(absent 99)=%d, want 3", got)
	}
}

func TestOrderIndependence(t *testing.T) {
	base := []key.K{7, 3, 7, 1, 9, 3, 4, 1, 8, 2, 6, 5}
	ref := buildList(t, key.MaxLevel, 100, base)
	for p := 1; p < len(base); p++ { // p 种循环移位排列
		perm := append(append([]key.K{}, base[p:]...), base[:p]...)
		got := buildList(t, key.MaxLevel, 100, perm)
		if !list.Equal(ref, got) {
			t.Fatalf("permutation %d built different structure", p)
		}
	}
}

func TestSpanInvariantUnderOps(t *testing.T) {
	for _, tc := range []struct {
		insert []key.K
		remove []key.K
	}{
		{[]key.K{5, 3, 8, 3, 1}, []key.K{3, 8}},
		{[]key.K{1, 2, 3, 4, 5, 6, 7, 8}, []key.K{1, 8, 4}},
		{[]key.K{9, 9, 9, 9}, []key.K{9, 9, 9}},
		{[]key.K{2, 7, 1, 8, 2, 8}, []key.K{2, 2, 8, 8, 7, 1}},
	} {
		l := buildList(t, key.MaxLevel, 100, tc.insert)
		if err := l.SelfCheck(); err != nil {
			t.Fatalf("after inserts %v: %v", tc.insert, err)
		}
		for _, k := range tc.remove {
			if err := l.Delete(k); err != nil {
				t.Fatal(err)
			}
			if err := l.SelfCheck(); err != nil {
				t.Fatalf("after delete %d of %v: %v", k, tc.insert, err)
			}
		}
	}
}
