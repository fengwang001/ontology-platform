package api_test

import (
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/hp"
	"ontology/hpi"
)

// genSeq builds n pseudo-random half-planes (LCG, coefficients in [-20,20]).
func genSeq(seed int64, n int) []hp.HalfPlane {
	s, out := seed, make([]hp.HalfPlane, 0, n)
	next := func() int { s = s*6364136223846793005 + 1442695040888963407; return int(s>>33)%41 - 20 }
	for len(out) < n {
		if a, b, c := next(), next(), next(); a != 0 || b != 0 {
			out = append(out, hp.HalfPlane{A: int64(a), B: int64(b), C: int64(c)})
		}
	}
	return out
}

var seqs = [][]hp.HalfPlane{
	{{A: -1}, {B: -1}, {A: 1, B: 1, C: 6}, {A: 1, C: 4}, {B: 1, C: 4}},
	{{A: -1}, {B: -1}, {A: 1, B: 1, C: 6}, {A: 1, C: 4}, {B: 1, C: 4}, {A: -1, C: -5}},
	genSeq(7, 30), genSeq(99, 40), genSeq(12345, 50),
}

// naiveClip is the independent reference: clip the bounding square by each
// half-plane in arrival order.
func naiveClip(seq []hp.HalfPlane) []hp.Point {
	b := int64(hpi.Bound)
	poly := []hp.Point{hp.Pt(-b, -b), hp.Pt(b, -b), hp.Pt(b, b), hp.Pt(-b, b)}
	for _, h := range seq {
		var out []hp.Point
		push := func(p hp.Point) {
			if len(out) == 0 || !hp.Eq(out[len(out)-1], p) {
				out = append(out, p)
			}
		}
		for i, p := range poly {
			q := poly[(i+1)%len(poly)]
			if hp.Inside(h, p) {
				push(p)
			}
			if hp.Inside(h, p) != hp.Inside(h, q) {
				if x, ok := hp.Intersect(h, p, q); ok {
					push(x)
				}
			}
		}
		if n := len(out); n > 1 && hp.Eq(out[0], out[n-1]) {
			out = out[:n-1]
		}
		poly = out
	}
	return poly
}

func run(t *testing.T, seq []hp.HalfPlane) []api.Point {
	t.Helper()
	_ = api.New()
	for _, h := range seq {
		if err := api.Add(int(h.A), int(h.B), int(h.C)); err != nil {
			t.Fatal(err)
		}
	}
	return api.Region()
}

func toAPI(vs []hp.Point) []api.Point {
	out := make([]api.Point, len(vs))
	for i, p := range vs {
		out[i].X, _ = p.X.Float64()
		out[i].Y, _ = p.Y.Float64()
	}
	return out
}

func sameCycleF(a, b []api.Point) bool {
	if len(a) != len(b) {
		return false
	}
	for s := range b {
		if slices.Equal(a, append(slices.Clone(b[s:]), b[:s]...)) {
			return true
		}
	}
	return len(a) == 0
}

func TestNaiveConsistency(t *testing.T) {
	for i, seq := range seqs {
		if got, want := run(t, seq), toAPI(naiveClip(seq)); !sameCycleF(got, want) {
			t.Fatalf("seq %d: got %v, want cycle of %v", i, got, want)
		}
	}
}

func TestRejectedInputNoSideEffect(t *testing.T) {
	bad := []struct {
		a, b, c int
		want    error
	}{{0, 0, 0, api.ErrDegenerate}, {0, 0, 7, api.ErrDegenerate}, {10001, 0, 0, api.ErrOutOfRange},
		{0, -10001, 0, api.ErrOutOfRange}, {1, 0, 10001, api.ErrOutOfRange}}
	if api.ErrDegenerate == api.ErrOutOfRange {
		t.Fatal("sentinel errors must be distinct")
	}
	_ = api.New()
	_ = api.Add(1, 0, 4)
	before := api.Region()
	for _, tc := range bad {
		if err := api.Add(tc.a, tc.b, tc.c); err != tc.want {
			t.Fatalf("Add(%d,%d,%d): got %v, want %v", tc.a, tc.b, tc.c, err, tc.want)
		}
	}
	if after := api.Region(); !sameCycleF(before, after) {
		t.Fatal("rejected input changed state")
	}
	if err := api.Add(0, 1, 4); err != nil || len(api.Region()) == 0 { // still usable
		t.Fatal("engine unusable after rejections")
	}
}

func TestConcurrentReadOnly(t *testing.T) {
	golden := run(t, seqs[0])
	var wg sync.WaitGroup
	var bad atomic.Int32
	for g := 0; g < 16; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if got := api.Region(); !sameCycleF(got, golden) || api.Empty() || api.SelfCheck() != nil {
					bad.Add(1)
					return
				}
			}
		}()
	}
	wg.Wait()
	if bad.Load() > 0 {
		t.Fatal("concurrent reads diverged")
	}
}
