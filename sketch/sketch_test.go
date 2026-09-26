package sketch

import (
	"errors"
	"math/rand"
	"testing"

	"ontology/ch"
)

func TestNewAndSentinels(t *testing.T) {
	for _, c := range [][2]int{{0, 3}, {6, 0}, {-1, 2}} {
		if _, err := New(c[0], c[1]); !errors.Is(err, ErrBadDimensions) {
			t.Fatalf("New(%d,%d) err=%v", c[0], c[1], err)
		}
	}
	s, _ := New(6, 3)
	badAdd := func(k, c int64, want error) {
		t.Helper()
		if err := s.Add(k, c); !errors.Is(err, want) {
			t.Fatalf("Add(%d,%d) err=%v want %v", k, c, err, want)
		}
	}
	badAdd(-1, 1, ErrBadKey)
	badAdd(1, 0, ErrBadCount)
	badAdd(1, -2, ErrBadCount)
	if _, err := s.Query(-1); !errors.Is(err, ErrBadKey) {
		t.Fatalf("Query(-1) err=%v", err)
	}
	if errors.Is(ErrBadDimensions, ErrBadCount) || errors.Is(ErrBadCount, ErrBadKey) ||
		errors.Is(ErrBadDimensions, ErrBadKey) {
		t.Fatal("the three sentinels must be mutually distinct")
	}
}
func stream(t *testing.T, seed int64, w, d, rounds int) (*Sketch, map[int64]int64) {
	t.Helper()
	s, err := New(w, d)
	if err != nil {
		t.Fatal(err)
	}
	exact := map[int64]int64{}
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < rounds; i++ {
		k, c := rng.Int63n(2000), rng.Int63n(9)+1
		if err := s.Add(k, c); err != nil {
			t.Fatal(err)
		}
		exact[k] += c
	}
	return s, exact
}
func TestNoUnderestimate(t *testing.T) {
	for _, c := range [][3]int{{6, 3, 50}, {31, 4, 500}, {256, 5, 3000}} {
		s, exact := stream(t, 1, c[0], c[1], c[2])
		for k, v := range exact {
			if g, err := s.Query(k); err != nil || g < v {
				t.Fatalf("w=%d Query(%d)=%d < true %d", c[0], k, g, v)
			}
		}
	}
}
func TestSingleKeyExact(t *testing.T) {
	for _, c := range [][4]int64{{6, 3, 2, 4}, {64, 3, 17, 123}, {1000, 7, 999999, 1 << 40}} {
		s, _ := New(int(c[0]), int(c[1]))
		if err := s.Add(c[2], c[3]); err != nil {
			t.Fatal(err)
		}
		if g, _ := s.Query(c[2]); g != c[3] {
			t.Fatalf("Query(%d)=%d want exact %d", c[2], g, c[3])
		}
	}
}
func TestMatchesExactMap(t *testing.T) {
	cases := []struct{ w, d, rounds, n int }{
		{8, 3, 100, 3}, {64, 3, 2000, 20}, {512, 5, 5000, 20},
	}
	for i, c := range cases {
		s, exact := stream(t, int64(i+7), c.w, c.d, c.rounds)
		for k, v := range exact { // with collisions: Query >= c(x)
			if g, _ := s.Query(k); g < v {
				t.Fatalf("Query(%d)=%d < exact %d", k, g, v)
			}
		}
		keys := collisionFreeSet(t, c.w, c.d, c.n) // collision-free: Query == c(x)
		s2, _ := New(c.w, c.d)
		for j, k := range keys {
			if err := s2.Add(k, int64(3*j+1)); err != nil {
				t.Fatal(err)
			}
		}
		for j, k := range keys {
			if g, _ := s2.Query(k); g != int64(3*j+1) {
				t.Fatalf("collision-free Query(%d)=%d want exact %d", k, g, 3*j+1)
			}
		}
	}
}
func collisionFreeSet(t *testing.T, w, d, n int) []int64 {
	t.Helper()
	f, err := ch.NewFamily(w)
	if err != nil {
		t.Fatal(err)
	}
	used := make([]map[int]bool, d)
	for j := range used {
		used[j] = map[int]bool{}
	}
	var keys []int64
	for x := int64(0); len(keys) < n; x++ {
		if x > int64(n*1000) {
			t.Fatalf("only found %d/%d collision-free keys", len(keys), n)
		}
		ok := true
		for j := 0; j < d; j++ {
			col := f.Column(j+1, x)
			if used[j][col] {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		keys = append(keys, x)
		for j := 0; j < d; j++ {
			used[j][f.Column(j+1, x)] = true
		}
	}
	return keys
}
func TestQueryTouchesExactlyDCells(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		w, d := 256, 5
		s, _ := New(w, d)
		for k := 0; k < m; k++ {
			if err := s.Add(int64(k), 1); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.Query(42); err != nil {
			t.Fatal(err)
		}
		// Read the unexported field directly; no exported accessor exists.
		if g := s.lastQueryProbes.Load(); g != int64(d) {
			t.Fatalf("m=%d probes=%d, want exactly %d", m, g, d)
		}
	}
}
