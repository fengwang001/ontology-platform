package match

import (
	"math/rand"
	"testing"

	"ontology/gh"
)

func P(x, y int64) gh.Point { return gh.Point{X: x, Y: y} }

var rm = [4][4]int64{{1, 0, 0, 1}, {0, -1, 1, 0}, {-1, 0, 0, -1}, {0, 1, -1, 0}}

func xf(p gh.Point, r, sc, tx, ty int64) gh.Point { // rotate r*90, scale, translate
	a := rm[r]
	return P((a[0]*p.X+a[1]*p.Y)*sc+tx, (a[2]*p.X+a[3]*p.Y)*sc+ty)
}
func tr(ts []gh.Point, r, sc, tx, ty int64) []gh.Point {
	o := make([]gh.Point, len(ts))
	for i, p := range ts {
		o[i] = xf(p, r, sc, tx, ty)
	}
	return o
}

var shapes = [][]gh.Point{
	{P(0, 0), P(4, 0), P(1, 3)},
	{P(0, 0), P(6, 0), P(2, 3), P(5, 1)},
	{P(0, 0), P(7, 0), P(1, 2), P(6, 3), P(3, 5)},
}

// brute is the exact quantization-free oracle over ordered bases.
func brute(t, s []gh.Point) bool {
	at := map[gh.Point]bool{}
	for _, p := range s {
		at[p] = true
	}
	n := len(t)
	for ai := range s {
		for bi := range s {
			if ai == bi {
				continue
			}
		tl:
			for ti := 0; ti < n; ti++ {
				for tj := 0; tj < n; tj++ {
					if ti == tj {
						continue
					}
					for q := 0; q < n; q++ {
						if q == ti || q == tj {
							continue
						}
						u, v, d := gh.BasisUV(t[ti], t[tj], t[q])
						dx, dy := s[bi].X-s[ai].X, s[bi].Y-s[ai].Y
						xn, yn := s[ai].X*d+u*dx-v*dy, s[ai].Y*d+u*dy+v*dx
						if d <= 0 || xn%d != 0 || yn%d != 0 || !at[P(xn/d, yn/d)] {
							continue tl
						}
					}
					return true
				}
			}
		}
	}
	return false
}

// TestCounterQuadratic reads the unexported vote counter directly
// (same package; no exported accessor). The collinear scene admits
// no match, so each ordered basis casts one vote before pruning:
// votes == n(n-1) <= 4*n^2 at each size 100..10000.
func TestCounterQuadratic(t *testing.T) {
	tmpl, err := NewTemplate([]Point{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 0, Y: 1}, {X: 1, Y: 2}})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{100, 1000, 10000} {
		scene := make([]Point, n)
		for i := range scene {
			scene[i] = Point{X: int64(i - n/2), Y: 0}
		}
		s := &session{}
		if m, f, err := tmpl.matchIn(s, scene); err != nil || f || m != nil {
			t.Fatalf("n=%d: f=%v err=%v", n, f, err)
		}
		if s.votes != int64(n*(n-1)) || s.votes > 4*int64(n)*int64(n) || s.votes > VoteBound(4, n) {
			t.Fatalf("n=%d: votes=%d", n, s.votes)
		}
	}
}

// TestInvariantExhaustiveConsistency pins found to the exact oracle
// on embedded similarity copies and on loop-generated random scenes.
func TestInvariantExhaustiveConsistency(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for c := 0; c < 6; c++ {
		tpl := shapes[c%3]
		tmpl, err := NewTemplate(tpl)
		if err != nil {
			t.Fatal(err)
		}
		var scn []gh.Point
		if c%2 == 0 { // index-aligned similarity copy
			scn = tr(tpl, r.Int63n(4), int64(1+r.Int63n(3)), r.Int63n(21)-10, r.Int63n(21)-10)
		} else { // random scene; the oracle is ground truth
			seen := map[gh.Point]bool{}
			for len(scn) < len(tpl)+5 {
				if p := P(r.Int63n(401)-200, r.Int63n(401)-200); !seen[p] {
					seen[p], scn = true, append(scn, p)
				}
			}
		}
		if _, f, err := tmpl.Match(scn); err != nil || f != brute(tpl, scn) {
			t.Fatalf("case %d: f=%v oracle=%v err=%v", c, f, brute(tpl, scn), err)
		}
	}
}

// TestInvariantSimilarity: one common similarity on template and
// scene leaves found and the mapping index-wise equal.
func TestInvariantSimilarity(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for c := 0; c < 3; c++ {
		tpl := shapes[c%3]
		t1, err := NewTemplate(tpl)
		if err != nil {
			t.Fatal(err)
		}
		s1 := tr(tpl, r.Int63n(4), int64(1+r.Int63n(3)), 0, 0)
		m1, f1, err := t1.Match(s1)
		if err != nil {
			t.Fatal(err)
		}
		gr, gs := r.Int63n(4), int64(1+r.Int63n(2))
		t2, err := NewTemplate(tr(tpl, gr, gs, 30, -20))
		if err != nil {
			t.Fatal(err)
		}
		m2, f2, _ := t2.Match(tr(s1, gr, gs, 30, -20))
		if f1 != f2 || len(m1) != len(m2) {
			t.Fatalf("case %d: result changed", c)
		}
		for i := range m1 {
			if m1[i] != m2[i] {
				t.Fatalf("case %d: mapping changed %v %v", c, m1, m2)
			}
		}
	}
}
