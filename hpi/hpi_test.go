package hpi

import (
	"errors"
	"math/big"
	"math/rand"
	"slices"
	"sync"
	"testing"

	"ontology/hp"
)

var fiveSteps = []hp.HalfPlane{{A: -1, B: 0, C: 0}, {A: 0, B: -1, C: 0}, {A: 1, B: 1, C: 6}, {A: 1, B: 0, C: 4}, {A: 0, B: 1, C: 4}}

func apply(seq []hp.HalfPlane) *Region {
	r := New()
	for _, h := range seq {
		if err := r.Add(h); err != nil {
			panic(err)
		}
	}
	return r
}

func randSeqs(seed int64, n int) [][]hp.HalfPlane {
	rng := rand.New(rand.NewSource(seed))
	out := make([][]hp.HalfPlane, n)
	for i := range out {
		for j := 0; j < 2+i%6; j++ {
			h := hp.HalfPlane{A: int64(rng.Intn(11) - 5), B: int64(rng.Intn(11) - 5), C: int64(rng.Intn(21) - 10)}
			if h.A != 0 || h.B != 0 {
				out[i] = append(out[i], h)
			}
		}
	}
	return out
}

func TestFiveSteps(t *testing.T) {
	if err := builtInSequence(); err != nil {
		t.Fatal(err)
	}
}

func TestNaiveConsistency(t *testing.T) {
	for _, seq := range randSeqs(1, 100) {
		r := apply(seq)
		naive := New().Verts()
		for _, h := range seq {
			naive = clip(naive, h, new(int))
		}
		if !slices.EqualFunc(r.Verts(), naive, hp.Eq) {
			t.Fatalf("seq %v: got %v, naive %v", seq, r.Verts(), naive)
		}
	}
}

func TestFeasibility(t *testing.T) {
	for _, seq := range randSeqs(2, 100) {
		vs := apply(seq).Verts()
		for _, p := range vs {
			for _, h := range seq {
				if hp.Side(h, p).Sign() > 0 {
					t.Fatalf("vert %v violates %v", p, h)
				}
			}
		}
		if err := gridCheck(seq, vs); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConvexCCW(t *testing.T) {
	for _, seq := range randSeqs(3, 100) {
		vs := apply(seq).Verts()
		for i := range vs {
			j := (i + 1) % len(vs)
			if hp.Eq(vs[i], vs[j]) {
				t.Fatalf("duplicate vert %d", i)
			}
			ex, ey := new(big.Rat).Sub(vs[j].X, vs[i].X), new(big.Rat).Sub(vs[j].Y, vs[i].Y)
			for k := 2; k < len(vs); k++ {
				w := vs[(i+k)%len(vs)]
				if cross2(ex, ey, new(big.Rat).Sub(w.X, vs[i].X), new(big.Rat).Sub(w.Y, vs[i].Y)).Sign() < 0 {
					t.Fatalf("non-convex/CCW at edge %d of %v", i, vs)
				}
			}
		}
	}
}

func TestRejectedInputLeavesState(t *testing.T) {
	r := apply(fiveSteps)
	before := r.Verts()
	bad := []hp.HalfPlane{{A: 0, B: 0, C: 5}, {A: 0, B: 0, C: 0}, {A: 10001, B: 0, C: 0}, {A: 0, B: -10001, C: 0}, {A: 1, B: 0, C: 10001}}
	want := []error{ErrDegenerate, ErrDegenerate, ErrOutOfRange, ErrOutOfRange, ErrOutOfRange}
	for i, h := range bad {
		if err := r.Add(h); !errors.Is(err, want[i]) {
			t.Fatalf("Add(%+v) = %v, want %v", h, err, want[i])
		}
		if !slices.EqualFunc(r.Verts(), before, hp.Eq) {
			t.Fatalf("state changed after rejected Add(%+v)", h)
		}
	}
	if errors.Is(ErrDegenerate, ErrOutOfRange) || errors.Is(ErrOutOfRange, ErrDegenerate) {
		t.Fatal("sentinel errors are not distinct")
	}
	if err := r.Add(hp.HalfPlane{A: 1, B: 0, C: 3}); err != nil {
		t.Fatalf("region unusable after rejections: %v", err)
	}
}

func TestRedundantEdgeCountBounded(t *testing.T) {
	box := []hp.HalfPlane{{A: -1, B: 0, C: 0}, {A: 0, B: -1, C: 0}, {A: 1, B: 0, C: 1}, {A: 0, B: 1, C: 1}}
	for _, m := range []int{100, 1000, 10000} {
		r := apply(box)
		for i := 0; i < m; i++ {
			h := hp.HalfPlane{A: 1 + int64(i%100), B: 1 + int64(i/100%100), C: 10000}
			if err := r.Add(h); err != nil {
				t.Fatal(err)
			}
			if r.edgesChecked != 0 {
				t.Fatalf("m=%d i=%d: edgesChecked=%d, want 0 (O(1) bbox precheck)", m, i, r.edgesChecked)
			}
		}
	}
}

func TestConcurrentReaders(t *testing.T) {
	r := apply(fiveSteps)
	want := r.Verts()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if !slices.EqualFunc(r.Verts(), want, hp.Eq) || len(r.Verts()) == 0 || r.SelfCheck() != nil {
					t.Error("concurrent read mismatch")
					return
				}
			}
		}()
	}
	wg.Wait()
}
