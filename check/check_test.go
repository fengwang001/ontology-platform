package check

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"testing"

	"ontology/id"
	"ontology/uf"
)

func TestSemantics(t *testing.T) {
	cases := []struct {
		name   string
		n      int
		unions [][2]int
		checks [][3]int // x, y, wantConnected
		count  int
	}{
		{"empty", 0, nil, nil, 0},
		{"singleton self", 1, nil, [][3]int{{0, 0, 1}}, 1},
		{"transitive", 3, [][2]int{{0, 1}, {1, 2}}, [][3]int{{0, 2, 1}, {0, 0, 1}}, 1},
		{"disjoint", 4, [][2]int{{0, 1}}, [][3]int{{0, 1, 1}, {2, 3, 0}, {1, 2, 0}}, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			set, err := uf.New(tc.n)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if got := set.Count(); got != tc.n {
				t.Fatalf("initial count = %d, want %d", got, tc.n)
			}
			for _, edge := range tc.unions {
				if _, err := set.Union(edge[0], edge[1]); err != nil {
					t.Fatalf("Union: %v", err)
				}
			}
			for _, c := range tc.checks {
				got, err := set.Connected(c[0], c[1])
				if err != nil || got != (c[2] == 1) {
					t.Fatalf("Connected(%d,%d)=%v,%v want %v", c[0], c[1], got, err, c[2] == 1)
				}
			}
			if got := set.Count(); got != tc.count {
				t.Fatalf("count = %d, want %d", got, tc.count)
			}
		})
	}
}

func TestUnionReturnAndErrors(t *testing.T) {
	type op struct{ x, y int }
	cases := []struct {
		name   string
		n      int
		unions []op
		want   []bool
	}{
		{"merge then repeat", 3, []op{{0, 1}, {0, 1}, {1, 2}}, []bool{true, false, true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			set, _ := uf.New(tc.n)
			for i, u := range tc.unions {
				got, err := set.Union(u.x, u.y)
				if err != nil || got != tc.want[i] {
					t.Fatalf("Union #%d = %v,%v want %v", i, got, err, tc.want[i])
				}
			}
		})
	}
	set, _ := uf.New(2)
	badCases := []func() error{
		func() error { _, e := set.Find(-1); return e },
		func() error { _, e := set.Find(2); return e },
		func() error { _, e := set.Union(0, 2); return e },
		func() error { _, e := set.Connected(-1, 0); return e },
	}
	for i, run := range badCases {
		if !errors.Is(run(), id.ErrBadIndex) {
			t.Fatalf("case %d: want ErrBadIndex", i)
		}
	}
	if _, err := uf.New(-1); !errors.Is(err, id.ErrNegativeSize) {
		t.Fatalf("New(-1): want ErrNegativeSize, got %v", err)
	}
	if got := (*uf.DSU)(nil).Count(); got != 0 {
		t.Fatalf("Count on nil = %d, want 0", got)
	}
	if _, err := (*uf.DSU)(nil).Find(0); !errors.Is(err, id.ErrNilReceiver) {
		t.Fatalf("nil Find: want ErrNilReceiver")
	}
}

func TestMatchesBFSReference(t *testing.T) {
	cases := []struct {
		name string
		n    int
		seed int64
		ops  int
	}{
		{"small dense", 8, 1, 30},
		{"medium sparse", 50, 7, 60},
		{"large", 200, 99, 300},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			set, _ := uf.New(tc.n)
			ref := NewReference(tc.n)
			rng := rand.New(rand.NewSource(tc.seed))
			for i := 0; i < tc.ops; i++ {
				x, y := rng.Intn(tc.n), rng.Intn(tc.n)
				got, _ := set.Union(x, y)
				if got != ref.Union(x, y) {
					t.Fatalf("Union(%d,%d) return diverged at op %d", x, y, i)
				}
			}
			for x := 0; x < tc.n; x++ {
				for y := 0; y < tc.n; y++ {
					got, _ := set.Connected(x, y)
					if got != ref.Connected(x, y) {
						t.Fatalf("Connected(%d,%d)=%v ref=%v", x, y, got, ref.Connected(x, y))
					}
				}
			}
			if set.Count() != ref.Count() {
				t.Fatalf("count %d != ref %d", set.Count(), ref.Count())
			}
		})
	}
}

type badDSU struct{ parent []int }

func newBad(n int) *badDSU {
	b := &badDSU{parent: make([]int, n)}
	for i := range b.parent {
		b.parent[i] = i
	}
	return b
}

func (b *badDSU) findHops(x int) int {
	hops := 0
	for b.parent[x] != x {
		x = b.parent[x]
		hops++
	}
	return hops
}

func (b *badDSU) union(x, y int) {
	root := func(v int) int {
		for b.parent[v] != v {
			v = b.parent[v]
		}
		return v
	}
	ry := root(y)
	b.parent[ry] = root(x) // always attach y-root under x-root
}

func TestHeightBounds(t *testing.T) {
	const n = 1 << 16
	ranked, _ := uf.New(n)
	bad := newBad(n)
	for i := 1; i < n; i++ {
		ranked.Union(i-1, i)
		bad.union(i, i-1)
	}
	if _, err := ranked.Find(n - 1); err != nil {
		t.Fatal(err)
	}
	if hops := ranked.LastFindHops(); hops > 16 {
		t.Fatalf("ranked chain hops = %d, want <= 16", hops)
	}
	if hops := bad.findHops(0); hops <= 1000 {
		t.Fatalf("unbalanced chain hops = %d, want > 1000", hops)
	}

	const rn = 10000
	rset, _ := uf.New(rn)
	rng := rand.New(rand.NewSource(20260926))
	for i := 0; i < 10000; i++ {
		rset.Union(rng.Intn(rn), rng.Intn(rn))
	}
	bound := int(2*math.Log2(rn)) + 2
	for i := 0; i < rn; i++ {
		rset.Find(i)
		if hops := rset.LastFindHops(); hops > bound {
			t.Fatalf("Find(%d) hops = %d, want <= %d", i, hops, bound)
		}
	}
}

func TestConcurrentReaders(t *testing.T) {
	const n = 1024
	set, _ := uf.New(n)
	for i := 1; i < n; i += 2 {
		set.Union(i-1, i)
	}
	want, err := set.Connected(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 5000; k++ {
				got, err := set.Connected(0, 1)
				if err != nil || got != want {
					t.Errorf("Connected=%v,%v want %v", got, err, want)
				}
				if set.Count() != n/2 {
					t.Errorf("Count = %d, want %d", set.Count(), n/2)
				}
			}
		}()
	}
	wg.Wait()
}
