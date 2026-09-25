package check

import (
	"errors"
	"math"
	"math/rand/v2"
	"sync"
	"testing"

	"ontology/id"
	"ontology/uf"
)

func TestNewCountBoundaries(t *testing.T) {
	tests := []struct {
		name string
		n    int
		want int
	}{{"zero", 0, 0}, {"one", 1, 1}, {"three", 3, 3}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set, err := uf.New(tt.n)
			if err != nil || set.Count() != tt.want {
				t.Fatalf("Count()=%d, err=%v", set.Count(), err)
			}
			if tt.n == 1 {
				if ok, err := set.Connected(0, 0); err != nil || !ok {
					t.Fatalf("self connected=%v, err=%v", ok, err)
				}
			}
		})
	}
}

func TestBadIndexesAreSentinel(t *testing.T) {
	tests := []struct {
		name string
		call func(*uf.DSU) error
	}{
		{"find", func(s *uf.DSU) error { _, err := s.Find(1); return err }},
		{"union", func(s *uf.DSU) error { _, err := s.Union(0, -1); return err }},
		{"connected", func(s *uf.DSU) error { _, err := s.Connected(1, 0); return err }},
		{"id", func(s *uf.DSU) error { _ = s; ids, _ := id.NewSet(1); _, err := ids.Find(id.Index(9)); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set, _ := uf.New(1)
			if err := tt.call(set); !errors.Is(err, uf.ErrBadIndex) || !errors.Is(err, id.ErrBadIndex) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestUnionMatchesReferenceAndTransitivity(t *testing.T) {
	set, ref := mustSets(t, 5)
	pairs := [][2]int{{0, 1}, {1, 2}, {3, 4}, {0, 1}, {2, 3}, {-1, 0}}
	for _, p := range pairs {
		got, gotErr := set.Union(p[0], p[1])
		want, wantErr := ref.Union(p[0], p[1])
		if got != want || !sameSentinel(gotErr, wantErr) || set.Count() != ref.Count() {
			t.Fatalf("Union(%v)=%v,%v want %v,%v count=%d/%d", p, got, gotErr, want, wantErr, set.Count(), ref.Count())
		}
	}
	tests := []struct{ x, y int }{{0, 1}, {0, 4}, {2, 3}, {0, 2}}
	for _, tt := range tests {
		got, err := set.Connected(tt.x, tt.y)
		want, _ := ref.Connected(tt.x, tt.y)
		if err != nil || got != want || !got {
			t.Fatalf("Connected(%d,%d)=%v,%v want %v", tt.x, tt.y, got, err, want)
		}
	}
}

func TestBalancedChainHops(t *testing.T) {
	const n = 1 << 16
	set, _ := mustSets(t, n)
	for i := 0; i+1 < n; i++ {
		merged, err := set.Union(i+1, i)
		if err != nil || !merged {
			t.Fatalf("Union(%d)=%v,%v", i, merged, err)
		}
	}
	if _, err := set.Find(0); err != nil || set.LastFindHops() > 16 {
		t.Fatalf("hops=%d, err=%v", set.LastFindHops(), err)
	}
	bad := newBad(n)
	for i := 0; i+1 < n; i++ {
		bad.union(i+1, i)
	}
	if bad.findHops(0) <= 1000 {
		t.Fatalf("bad hops=%d", bad.findHops(0))
	}
}

func TestRandomHopsBound(t *testing.T) {
	const n = 2048
	set, _ := mustSets(t, n)
	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 10000; i++ {
		_, _ = set.Union(rng.IntN(n), rng.IntN(n))
	}
	bound := int(2*math.Log2(float64(n)) + 2)
	for x := 0; x < n; x++ {
		if _, err := set.Find(x); err != nil || set.LastFindHops() > bound {
			t.Fatalf("x=%d hops=%d bound=%d", x, set.LastFindHops(), bound)
		}
	}
}

func TestConcurrentReads(t *testing.T) {
	set, _ := mustSets(t, 128)
	for i := 0; i+1 < 64; i++ {
		_, _ = set.Union(i, i+1)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if set.Count() != 65 {
					t.Errorf("count changed")
				}
				if ok, _ := set.Connected(0, 63); !ok {
					t.Errorf("connected changed")
				}
			}
		}()
	}
	wg.Wait()
}

type badDSU struct{ parent []int }

func newBad(n int) *badDSU {
	b := &badDSU{parent: make([]int, n)}
	for i := range b.parent {
		b.parent[i] = i
	}
	return b
}

func (b *badDSU) find(x int) int {
	for b.parent[x] != x {
		x = b.parent[x]
	}
	return x
}

func (b *badDSU) findHops(x int) int {
	hops := 0
	for b.parent[x] != x {
		x = b.parent[x]
		hops++
	}
	return hops
}

func (b *badDSU) union(x, y int) { b.parent[b.find(y)] = b.find(x) }

func mustSets(t *testing.T, n int) (*uf.DSU, *Reference) {
	t.Helper()
	set, err := uf.New(n)
	if err != nil {
		t.Fatal(err)
	}
	return set, NewReference(n)
}

func sameSentinel(a, b error) bool { return errors.Is(a, b) && errors.Is(b, a) }
