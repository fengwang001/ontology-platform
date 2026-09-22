package postings

import (
	"errors"
	"testing"
)

func build(t *testing.T, docs map[uint64][]int) *List {
	t.Helper()
	l := New()
	for docID, ps := range docs {
		if err := l.Add(docID, ps); err != nil {
			t.Fatalf("add %d: %v", docID, err)
		}
	}
	return l
}

func TestAddRemoveSet(t *testing.T) {
	t.Run("ordered insert and duplicate", func(t *testing.T) {
		l := New()
		if err := l.Add(5, []int{1, 3}); err != nil {
			t.Fatal(err)
		}
		if err := l.Add(1, []int{0}); err != nil {
			t.Fatal(err)
		}
		if err := l.Add(5, nil); !errors.Is(err, ErrDuplicateDoc) {
			t.Fatalf("got %v, want ErrDuplicateDoc", err)
		}
		if got := l.DocIDs(); len(got) != 2 || got[0] != 1 || got[1] != 5 {
			t.Fatalf("DocIDs = %v", got)
		}
		if err := l.SelfCheck(); err != nil {
			t.Fatal(err)
		}
	})
	cases := []struct {
		name    string
		docs    map[uint64][]int
		remove  []uint64
		set     map[uint64][]int
		wantIDs []uint64
		wantPos map[uint64][]int
	}{
		{"remove middle", map[uint64][]int{1: {0}, 2: {1}, 3: {2}},
			[]uint64{2}, nil, []uint64{1, 3}, map[uint64][]int{1: {0}, 3: {2}}},
		{"remove to empty", map[uint64][]int{7: {0, 1}},
			[]uint64{7}, nil, nil, nil},
		{"set replaces positions", map[uint64][]int{9: {0, 1, 2}},
			nil, map[uint64][]int{9: {5}}, []uint64{9}, map[uint64][]int{9: {5}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := build(t, tc.docs)
			for _, id := range tc.remove {
				if err := l.Remove(id); err != nil {
					t.Fatalf("remove %d: %v", id, err)
				}
			}
			for id, ps := range tc.set {
				if err := l.Set(id, ps); err != nil {
					t.Fatalf("set %d: %v", id, err)
				}
			}
			got := l.DocIDs()
			if len(got) != len(tc.wantIDs) {
				t.Fatalf("DocIDs = %v, want %v", got, tc.wantIDs)
			}
			for i := range got {
				if got[i] != tc.wantIDs[i] {
					t.Fatalf("DocIDs = %v, want %v", got, tc.wantIDs)
				}
			}
			for id, want := range tc.wantPos {
				ps, ok := l.Positions(id)
				if !ok || len(ps) != len(want) {
					t.Fatalf("positions %d = %v (%v), want %v", id, ps, ok, want)
				}
				for k := range ps {
					if ps[k] != want[k] {
						t.Fatalf("positions %d = %v, want %v", id, ps, want)
					}
				}
			}
			if err := l.SelfCheck(); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("missing remove and set", func(t *testing.T) {
		l := New()
		if err := l.Remove(1); !errors.Is(err, ErrDocNotFound) {
			t.Fatalf("remove got %v", err)
		}
		if err := l.Set(1, nil); !errors.Is(err, ErrDocNotFound) {
			t.Fatalf("set got %v", err)
		}
	})
}

func TestSetAlgebra(t *testing.T) {
	cases := []struct {
		name                 string
		left, right          map[uint64][]int
		wantInter, wantUnion []uint64
		wantDifference       []uint64
	}{
		{"overlap", map[uint64][]int{1: nil, 2: nil, 3: nil},
			map[uint64][]int{2: nil, 3: nil, 4: nil},
			[]uint64{2, 3}, []uint64{1, 2, 3, 4}, []uint64{1}},
		{"disjoint", map[uint64][]int{1: nil}, map[uint64][]int{2: nil},
			nil, []uint64{1, 2}, []uint64{1}},
		{"empty side", map[uint64][]int{1: nil, 2: nil}, nil,
			nil, []uint64{1, 2}, []uint64{1, 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, r := build(t, tc.left), build(t, tc.right)
			assertIDs(t, "intersect", l.Intersect(r), tc.wantInter)
			assertIDs(t, "union", l.Union(r), tc.wantUnion)
			assertIDs(t, "difference", l.Difference(r), tc.wantDifference)
		})
	}
}

func assertIDs(t *testing.T, op string, got, want []uint64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", op, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", op, got, want)
		}
	}
}

// TestIntersectScaling 钉住第四节：比较次数不随长侧长度线性增长。
// 输出 SCALING 行供 cmd/demo 子进程解析。
func TestIntersectScaling(t *testing.T) {
	cases := []struct {
		name string
		long int
	}{
		{"1k", 1000},
		{"100k", 100000},
	}
	got := map[string]int64{}
	for _, tc := range cases {
		shortIDs := []uint64{10, 500, 990}
		if tc.long == 100000 {
			shortIDs = []uint64{10, 20000, 90000}
		}
		short := New()
		for _, id := range shortIDs {
			if err := short.Add(id, nil); err != nil {
				t.Fatal(err)
			}
		}
		long := New()
		for id := uint64(1); int(id) <= tc.long; id++ {
			if err := long.Add(id, nil); err != nil {
				t.Fatal(err)
			}
		}
		res := short.Intersect(long)
		if len(res) != 3 {
			t.Fatalf("%s: intersect = %v, want 3 hits", tc.name, res)
		}
		got[tc.name] = lastMergeComparisons.Load()
	}
	t.Logf("SCALING short=3 long=1000 comparisons=%d long=100000 comparisons=%d",
		got["1k"], got["100k"])
	if got["100k"] >= got["1k"]*10 {
		t.Fatalf("comparisons grow near-linearly: %d -> %d", got["1k"], got["100k"])
	}
}
