package api

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func buildS(t *testing.T, pts [][2]int) *Service {
	t.Helper()
	s, _ := New()
	for _, p := range pts {
		if err := s.Insert(p[0], p[1]); err != nil {
			t.Fatalf("insert %v: %v", p, err)
		}
	}
	return s
}

// TestSentinelErrors: the three failures are distinct, decidable via
// errors.Is, and a rejected call leaves every observable state unchanged.
func TestSentinelErrors(t *testing.T) {
	if ErrDuplicatePoint == ErrOutOfBounds || ErrDuplicatePoint == ErrCollinearStart ||
		ErrOutOfBounds == ErrCollinearStart {
		t.Fatal("sentinel errors must be pairwise distinct")
	}
	s := buildS(t, [][2]int{{0, 0}, {4, 0}, {0, 4}, {4, 4}})
	cases := []struct {
		x, y int
		want error
	}{
		{4, 0, ErrDuplicatePoint},
		{10001, 0, ErrOutOfBounds},
		{-10001, 7, ErrOutOfBounds},
		{0, 10001, ErrOutOfBounds},
	}
	for _, c := range cases {
		before := s.Triangles()
		if !errors.Is(s.Insert(c.x, c.y), c.want) {
			t.Fatalf("(%d,%d): want %v", c.x, c.y, c.want)
		}
		if !reflect.DeepEqual(before, s.Triangles()) {
			t.Fatalf("(%d,%d): rejected insert changed state", c.x, c.y)
		}
	}
	// collinear first three; the refused third point creates no triangle and
	// the service stays usable.
	c, _ := New()
	if err := c.Insert(0, 0); err != nil || c.Insert(2, 2) != nil {
		t.Fatal("setup")
	}
	if !errors.Is(c.Insert(5, 5), ErrCollinearStart) || len(c.Triangles()) != 0 {
		t.Fatal("collinear start not rejected without trace")
	}
	if err := c.Insert(0, 5); err != nil {
		t.Fatalf("unusable after collinear refusal: %v", err)
	}
}

// TestConcurrentReaders: many goroutines concurrently read one populated
// instance; every result must be field-for-field identical (no sleeps).
func TestConcurrentReaders(t *testing.T) {
	pts := [][2]int{{0, 0}, {4, 0}, {0, 4}, {4, 4}, {3, 3}, {2, 1},
		{-3, 2}, {5, -2}, {-4, -4}, {6, 6}}
	s := buildS(t, pts)
	const n = 16
	var wg sync.WaitGroup
	tris := make([][][3]Point, n)
	hulls := make([][]Point, n)
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tris[i] = s.Triangles()
			hulls[i] = s.Hull()
		}(g)
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if !reflect.DeepEqual(tris[0], tris[i]) {
			t.Fatalf("reader %d triangles differ", i)
		}
		if !reflect.DeepEqual(hulls[0], hulls[i]) {
			t.Fatalf("reader %d hull differs", i)
		}
	}
	if s.SelfCheck() != nil {
		t.Fatal("self-check failed on concurrently read instance")
	}
}

// TestHullCCW: hull vertices are strictly counter-clockwise and every hull
// segment is an actual triangulation boundary edge.
func TestHullCCW(t *testing.T) {
	s := buildS(t, [][2]int{{0, 0}, {4, 0}, {0, 4}, {4, 4}, {2, 2}})
	h := s.Hull()
	if len(h) != 4 {
		t.Fatalf("hull has %d vertices, want 4", len(h))
	}
	edge := map[[2]Point]bool{}
	for _, tr := range s.Triangles() {
		for k := 0; k < 3; k++ {
			edge[norm(tr[(k+1)%3], tr[(k+2)%3])] = true
		}
	}
	for i := range h {
		a, b := h[i], h[(i+1)%len(h)]
		if !edge[norm(a, b)] {
			t.Fatalf("hull segment %v-%v is not a triangulation edge", a, b)
		}
	}
}
