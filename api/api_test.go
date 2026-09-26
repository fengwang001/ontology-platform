package api_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
)

func p(x, y int) api.Point { return api.Point{X: x, Y: y} }

// TestRejectionNoParallel: the four rejection classes are distinguishable,
// every rejection is total, and the API stays usable afterwards.
func TestRejectionNoPartial(t *testing.T) {
	cases := []struct {
		name string
		q    []api.Point
		want error
	}{
		{"too-few", []api.Point{p(0, 0), p(1, 0)}, api.ErrTooFewVertices},
		{"self-intersect", []api.Point{p(0, 0), p(4, 0), p(4, 4), p(2, -1), p(0, 4)}, api.ErrSelfIntersecting},
		{"duplicate", []api.Point{p(0, 0), p(0, 0), p(1, 0), p(1, 1), p(0, 1)}, api.ErrNotCounterClockwise},
		{"clockwise", []api.Point{p(0, 0), p(0, 3), p(3, 3), p(3, 0)}, api.ErrNotCounterClockwise},
		{"out-of-range", []api.Point{p(0, 0), p(10001, 0), p(10001, 1), p(0, 1)}, api.ErrCoordinateOutOfRange},
	}
	seen := map[error]bool{}
	for _, c := range cases {
		poly, err := api.NewPolygon(c.q)
		if !errors.Is(err, c.want) {
			t.Fatalf("%s: err=%v want %v", c.name, err, c.want)
		}
		if poly != nil {
			t.Fatalf("%s: rejection produced a partial polygon", c.name)
		}
		seen[c.want] = true
	}
	if len(seen) < 4 { // four rejection classes must be pairwise distinct
		t.Fatalf("sentinel errors are not distinct: %d classes", len(seen))
	}
	good := []api.Point{p(0, 0), p(3, 0), p(3, 3), p(0, 3)}
	if _, err := api.NewPolygon(good); err != nil {
		t.Fatalf("API unusable after rejections: %v", err)
	}
}

// TestValidUnionAndArea exercises the headline example through the facade.
func TestValidUnionAndArea(t *testing.T) {
	a, _ := api.NewPolygon([]api.Point{p(0, 0), p(3, 0), p(3, 3), p(0, 3)})
	b, _ := api.NewPolygon([]api.Point{p(2, 2), p(5, 2), p(5, 5), p(2, 5)})
	u, err := a.Union(b)
	if err != nil {
		t.Fatal(err)
	}
	want := []api.Point{p(0, 0), p(3, 0), p(3, 2), p(5, 2), p(5, 5), p(2, 5), p(2, 3), p(0, 3)}
	if got := u.Vertices(); !reflect.DeepEqual(got, want) {
		t.Fatalf("union=%v want %v", got, want)
	}
	if err := a.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestConcurrentReaders: many goroutines only read one finished union; every
// Vertices snapshot must be field-for-field identical (no sleeps).
func TestConcurrentReaders(t *testing.T) {
	a, _ := api.NewPolygon([]api.Point{p(0, 0), p(3, 0), p(3, 3), p(0, 3)})
	b, _ := api.NewPolygon([]api.Point{p(2, 2), p(5, 2), p(5, 5), p(2, 5)})
	u, err := a.Union(b)
	if err != nil {
		t.Fatal(err)
	}
	ref := u.Vertices()
	const n = 64
	var wg sync.WaitGroup
	errs := make(chan error, n*2)
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if got := u.Vertices(); !reflect.DeepEqual(got, ref) {
				errs <- errors.New("Vertices mismatch")
			}
		}()
		go func() {
			defer wg.Done()
			if err := u.SelfCheck(); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}
