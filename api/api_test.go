package api_test

import (
	"errors"
	"testing"

	"ontology/api"
	"ontology/nbr"
	"ontology/sites"
)

// TestDist2Exact nails invariant 2 at the geometry layer: exact int64 d2.
func TestDist2Exact(t *testing.T) {
	cases := []struct {
		d    int64
		p, q nbr.Point
	}{
		{25, nbr.Point{X: 0, Y: 0}, nbr.Point{X: 3, Y: 4}},
		{25, nbr.Point{X: -3, Y: -4}, nbr.Point{X: 0, Y: 0}},
		{0, nbr.Point{X: -7, Y: 13}, nbr.Point{X: -7, Y: 13}},
		{800000000, nbr.Point{X: -10000, Y: -10000}, nbr.Point{X: 10000, Y: 10000}},
	}
	for _, c := range cases {
		if g := nbr.Dist2(c.p, c.q); g != c.d || nbr.Dist2(c.q, c.p) != c.d {
			t.Errorf("Dist2=%d want %d", g, c.d)
		}
	}
}

// TestWithinStrict nails invariant 2: d2 == r2 on the circle is excluded.
func TestWithinStrict(t *testing.T) {
	for _, c := range []struct {
		d2, r2 int64
		in     bool
	}{{0, 1, true}, {24, 25, true}, {25, 25, false}, {26, 25, false}, {0, 0, false}} {
		if nbr.Inside(c.d2, c.r2) != c.in {
			t.Errorf("Inside(%d,%d)", c.d2, c.r2)
		}
	}
}

// TestEightStepSequence nails section 3 steps 1..8 through the public api.
func TestEightStepSequence(t *testing.T) {
	if err := api.New(); err != nil {
		t.Fatal(err)
	}
	adds := [][3]int{{0, 0, 0}, {1, 1, 1}, {4, 0, 2}, {0, 2, 3}}
	for k := 0; k < 3; k++ {
		if idx, err := api.Add(adds[k][0], adds[k][1]); err != nil || idx != adds[k][2] {
			t.Fatalf("step %d Add: %d,%v", k+1, idx, err)
		}
	}
	for _, c := range []struct {
		name         string
		qx, qy, want int
	}{
		{"step4 Nearest(0,2)", 0, 2, 1},
		{"step5 Nearest(1,0)", 1, 0, 0},
	} {
		if n, err := api.Nearest(c.qx, c.qy); err != nil || n != c.want {
			t.Fatalf("%s: got %d,%v want %d", c.name, n, err, c.want)
		}
	}
	if idx, err := api.Add(adds[3][0], adds[3][1]); err != nil || idx != 3 {
		t.Fatalf("step6 Add: %d,%v", idx, err)
	}
	if n, err := api.Nearest(0, 2); err != nil || n != 3 {
		t.Fatalf("step7 Nearest: %d,%v want 3", n, err)
	}
	if n, err := api.Within(1, 0, 1); err != nil || n != 0 {
		t.Fatalf("step8 Within: %d,%v want 0", n, err)
	}
}

// TestSelfCheck exercises the public self-check covering all four invariants.
func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestAPIRejects nails section 5: the three sentinels are distinct, rejected
// calls leave no trace, and the service stays usable afterwards.
func TestAPIRejects(t *testing.T) {
	if err := api.New(); err != nil {
		t.Fatal(err)
	}
	if idx, err := api.Add(3, 4); err != nil || idx != 0 {
		t.Fatalf("first Add: %d,%v", idx, err)
	}
	for _, c := range []struct {
		name string
		want error
		run  func() error
	}{
		{"duplicate", api.ErrDuplicateSite, func() error { _, e := api.Add(3, 4); return e }},
		{"oob x", api.ErrCoordinateOutOfBound, func() error { _, e := api.Add(10001, 0); return e }},
		{"oob y", api.ErrCoordinateOutOfBound, func() error { _, e := api.Add(0, -10001); return e }},
		{"negative r", api.ErrNegativeRadius, func() error { _, e := api.Within(0, 0, -1); return e }},
	} {
		if err := c.run(); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v want %v", c.name, err, c.want)
		}
	}
	for _, p := range [][2]error{
		{api.ErrDuplicateSite, api.ErrCoordinateOutOfBound},
		{api.ErrDuplicateSite, api.ErrNegativeRadius},
		{api.ErrCoordinateOutOfBound, api.ErrNegativeRadius},
	} {
		if errors.Is(p[0], p[1]) {
			t.Fatalf("api sentinels not distinct: %v / %v", p[0], p[1])
		}
	}
	if n, err := api.Within(0, 0, 10); err != nil || n != 1 {
		t.Fatalf("state leaked from rejected add: Within=%d,%v", n, err)
	}
	if _, err := api.Add(-10000, 10000); err != nil {
		t.Fatalf("service unusable after rejections: %v", err)
	}
	if err := api.New(); err != nil {
		t.Fatal(err)
	}
	if _, err := api.Nearest(0, 0); !errors.Is(err, sites.ErrEmpty) {
		t.Fatalf("Nearest on empty: got %v want sites.ErrEmpty", err)
	}
}

// TestNewResets verifies New() restores a clean, reusable process-wide state.
func TestNewResets(t *testing.T) {
	if err := api.New(); err != nil {
		t.Fatal(err)
	}
	if _, err := api.Add(0, 0); err != nil {
		t.Fatal(err)
	}
	if err := api.New(); err != nil {
		t.Fatal(err)
	}
	if idx, err := api.Add(5, 5); err != nil || idx != 0 {
		t.Fatalf("after New index must restart at 0: got %d,%v", idx, err)
	}
}
