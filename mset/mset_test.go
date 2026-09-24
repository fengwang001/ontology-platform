package mset_test

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"ontology/key"
	"ontology/list"
	"ontology/mset"
	"ontology/rank"
)

func build(t *testing.T, lim mset.Limits, keys []key.K) *mset.Mset {
	t.Helper()
	m, err := mset.New(lim)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if err := m.Insert(k); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

func TestMultisetSemantics(t *testing.T) {
	full := mset.Limits{MaxLevel: key.MaxLevel, MaxElems: 100}
	for _, tc := range []struct {
		keys                           []key.K
		probe                          key.K
		rank, cnt, rankAfter, cntAfter int
	}{
		{[]key.K{5, 5, 5}, 5, 0, 3, 0, 2},
		{[]key.K{1, 3, 3, 2}, 3, 2, 2, 2, 1},
		{[]key.K{1, 3, 3, 2}, 4, 4, 0, 4, 0},
		{[]key.K{9, 1, 9, 1, 9}, 9, 2, 3, 2, 2},
	} {
		m := build(t, full, tc.keys)
		if got := m.RankOf(tc.probe); got != tc.rank {
			t.Fatalf("%v RankOf(%d)=%d, want %d", tc.keys, tc.probe, got, tc.rank)
		}
		if got := m.Count(tc.probe); got != tc.cnt {
			t.Fatalf("%v Count(%d)=%d, want %d", tc.keys, tc.probe, got, tc.cnt)
		}
		if tc.cnt > 0 {
			if err := m.Delete(tc.probe); err != nil {
				t.Fatal(err)
			}
			if got := m.RankOf(tc.probe); got != tc.rankAfter {
				t.Fatalf("%v after delete RankOf(%d)=%d, want %d", tc.keys, tc.probe, got, tc.rankAfter)
			}
			if got := m.Count(tc.probe); got != tc.cntAfter {
				t.Fatalf("%v after delete Count(%d)=%d, want %d", tc.keys, tc.probe, got, tc.cntAfter)
			}
			if err := m.SelfCheck(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestIteratorSnapshot(t *testing.T) {
	m := build(t, mset.Limits{MaxLevel: key.MaxLevel, MaxElems: 100}, []key.K{4, 1, 4, 2})
	it := m.Iterate(1)
	var got []key.K
	for it.Next() {
		got = append(got, it.Key())
		_ = m.Delete(it.Key()) // 删除迭代器当前指向的元素
	}
	want := []key.K{2, 4, 4}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if m.Size() != 1 {
		t.Fatalf("size=%d, want 1", m.Size())
	}
}

func TestConcurrentReads(t *testing.T) {
	m := build(t, mset.Limits{MaxLevel: key.MaxLevel, MaxElems: 1000}, []key.K{5, 1, 9, 3, 7, 3, 5})
	const g = 16
	results := make([][]uint64, g)
	var wg sync.WaitGroup
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < m.Size(); j++ {
				k, err := m.At(j)
				if err != nil {
					t.Error(err)
					return
				}
				n, _ := m.Range(k, 8)
				results[i] = append(results[i], uint64(k), uint64(m.RankOf(k)), uint64(m.Count(k)), uint64(n))
			}
			for it := m.Iterate(0); it.Next(); {
				results[i] = append(results[i], uint64(it.Key()))
			}
		}(i)
	}
	wg.Wait()
	for i := 1; i < g; i++ {
		if len(results[i]) != len(results[0]) {
			t.Fatalf("goroutine %d got %d results, want %d", i, len(results[i]), len(results[0]))
		}
		for j := range results[i] {
			if results[i][j] != results[0][j] {
				t.Fatalf("goroutine %d result %d differs", i, j)
			}
		}
	}
}

func TestLimitsAndErrors(t *testing.T) {
	if _, err := mset.New(mset.Limits{MaxLevel: 0, MaxElems: 1}); !errors.Is(err, list.ErrBadConfig) {
		t.Fatalf("bad config err=%v", err)
	}
	m := build(t, mset.Limits{MaxLevel: key.MaxLevel, MaxElems: 2}, []key.K{1, 2})
	if err := m.Insert(3); !errors.Is(err, list.ErrTooManyElements) {
		t.Fatalf("overflow err=%v", err)
	}
	if err := m.Delete(9); !errors.Is(err, list.ErrNotFound) {
		t.Fatalf("delete absent err=%v", err)
	}
	if _, err := m.Range(2, 1); !errors.Is(err, rank.ErrBadRange) {
		t.Fatalf("bad range err=%v", err)
	}
	narrow := build(t, mset.Limits{MaxLevel: 1, MaxElems: 10}, nil)
	hk := key.K(0)
	for key.Level(hk) <= 1 {
		hk++
	}
	if err := narrow.Insert(hk); !errors.Is(err, list.ErrLevelLimit) {
		t.Fatalf("level limit err=%v", err)
	}
	if err := m.Delete(1); err != nil || m.Size() != 1 {
		t.Fatalf("after rejection: delete err=%v size=%d", err, m.Size())
	}
	lk := key.K(0)
	for key.Level(lk) != 1 {
		lk++
	}
	if err := narrow.Insert(lk); err != nil || narrow.Size() != 1 {
		t.Fatalf("narrow unusable after rejection: %v", err)
	}
}
