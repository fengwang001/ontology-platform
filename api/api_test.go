package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
	"ontology/gh"
)

func P(x, y int64) gh.Point { return gh.Point{X: x, Y: y} }

var rm = [4][4]int64{{1, 0, 0, 1}, {0, -1, 1, 0}, {-1, 0, 0, -1}, {0, 1, -1, 0}}

// TestCanonicalCoordinates checks the required four-row table of
// canonical coordinates for square T and its R90 scene (NOTES §3).
func TestCanonicalCoordinates(t *testing.T) {
	tpl := []gh.Point{P(0, 0), P(2, 0), P(2, 2), P(0, 2)}
	scn := []gh.Point{P(5, 5), P(5, 7), P(3, 7), P(3, 5)}
	want := []gh.Key{{U: 0, V: 0}, {U: 16, V: 0}, {U: 16, V: 16}, {U: 0, V: 16}}
	for i := range want {
		if k := gh.KeyOf(tpl[0], tpl[1], tpl[i]); k != want[i] {
			t.Fatalf("T row %d: %v", i, k)
		}
		if k := gh.KeyOf(scn[0], scn[1], scn[i]); k != want[i] {
			t.Fatalf("S row %d: %v", i, k)
		}
	}
}

// TestMirrorRejected: scalene (chiral) triangle reflection rejects;
// its true rotated copy matches (orientation preserved, no
// conjugate anywhere in the verifier).
func TestMirrorRejected(t *testing.T) {
	tpl := []gh.Point{P(0, 0), P(4, 0), P(1, 3)}
	if err := api.NewTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	if _, f, _ := api.Match([]gh.Point{P(5, 5), P(9, 5), P(6, 2)}); f {
		t.Fatal("mirror copy matched")
	}
	rot := make([]gh.Point, len(tpl)) // R90 + (50,50), index-aligned
	for i, p := range tpl {
		a := rm[1]
		rot[i] = P(a[0]*p.X+a[1]*p.Y+50, a[2]*p.X+a[3]*p.Y+50)
	}
	m, f, err := api.Match(rot)
	if err != nil || !f || m[0] != 0 || m[1] != 1 || m[2] != 2 {
		t.Fatalf("rotated copy: m=%v f=%v err=%v", m, f, err)
	}
}

// TestRejectedInputs: the five distinct sentinels; a rejected Match
// leaves the installed template fully usable (no partial result).
func TestRejectedInputs(t *testing.T) {
	for _, b := range []struct {
		pts []gh.Point
		err error
	}{
		{[]gh.Point{P(0, 0), P(1, 1)}, api.ErrTooFewPoints},
		{[]gh.Point{P(0, 0), P(1, 1), P(2, 2)}, api.ErrCollinear},
		{[]gh.Point{P(0, 0), P(1, 0), P(0, 1), P(0, 0)}, api.ErrDuplicatePoint},
		{[]gh.Point{P(0, 0), P(1, 0), P(0, 10001)}, api.ErrOutOfRange},
	} {
		if err := api.NewTemplate(b.pts); !errors.Is(err, b.err) {
			t.Fatalf("got %v want %v", err, b.err)
		}
	}
	if err := api.NewTemplate([]gh.Point{P(0, 0), P(2, 0), P(2, 2), P(0, 2)}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := api.Match([]gh.Point{P(0, 0)}); !errors.Is(err, api.ErrSceneTooSmall) {
		t.Fatal(err)
	}
	rot := []gh.Point{P(5, 5), P(5, 7), P(3, 7), P(3, 5)}
	if m, f, err := api.Match(rot); err != nil || !f || m[2] != 2 {
		t.Fatalf("unusable after rejection: m=%v f=%v err=%v", m, f, err)
	}
	if err := api.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentMatch: goroutines read one template concurrently;
// results are field-by-field identical. No sleep used.
func TestConcurrentMatch(t *testing.T) {
	if err := api.NewTemplate([]gh.Point{P(0, 0), P(2, 0), P(2, 2), P(0, 2)}); err != nil {
		t.Fatal(err)
	}
	scn := []gh.Point{P(5, 5), P(5, 7), P(3, 7), P(3, 5)}
	const gN = 16
	res, found := make([][]int, gN), make([]bool, gN)
	var wg sync.WaitGroup
	for g := 0; g < gN; g++ {
		wg.Add(1)
		go func(g int) { defer wg.Done(); res[g], found[g], _ = api.Match(scn) }(g)
	}
	wg.Wait()
	for g := 1; g < gN; g++ {
		if found[g] != found[0] || len(res[g]) != len(res[0]) {
			t.Fatalf("goroutine %d differs", g)
		}
		for i := range res[0] {
			if res[g][i] != res[0][i] {
				t.Fatalf("goroutine %d mapping differs", g)
			}
		}
	}
}
