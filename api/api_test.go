package api

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func ck(t *testing.T, err error) {
	if err != nil {
		t.Fatal(err)
	}
}

// TestSevenStepSequence pins the NOTES.md seven-step table and final stats.
func TestSevenStepSequence(t *testing.T) {
	x, _ := New(10, 4, 3)
	ids := []string{"A", "B", "C", "D", "E", "F", "G"}
	bs := []int64{0, 20, 40, 50, 70, 90, 100}
	es := []int64{10, 35, 45, 61, 82, 94, 120}
	wantBr := []bool{false, false, false, false, true, false, true}
	wantV := []int64{0, 1, 1, 2, 3, 3, 4}
	for i := range ids {
		ck(t, x.Begin(ids[i], bs[i]))
		ck(t, x.End(ids[i], es[i]))
		if x.Violations() != wantV[i] || x.Breached() != wantBr[i] {
			t.Fatalf("step %s: v=%d br=%v want %d/%v", ids[i], x.Violations(), x.Breached(), wantV[i], wantBr[i])
		}
	}
	c, mn, mx, avg, inf := x.Count(), x.MinLatency(), x.MaxLatency(), x.AvgLatency(), x.InFlight()
	if c != 7 || x.Violations() != 4 || mn != 4 || mx != 20 || avg != 11 || inf != 0 {
		t.Fatalf("stats c=%d v=%d min=%d max=%d avg=%v inf=%d", c, x.Violations(), mn, mx, avg, inf)
	}
}

// TestBatchRecompute pins invariant 1: live stats equal an independent batch
// recompute over random interleaved Begin/End sequences with some in flight.
func TestBatchRecompute(t *testing.T) {
	for _, T := range []int64{0, 1, 10, 100} {
		for tr := 0; tr < 30; tr++ {
			rng := rand.New(rand.NewSource(int64(T)*1000 + int64(tr)))
			x, _ := New(T, 5, 2)
			var sum, viol, mx int64
			mn, done, inf := int64(1<<63-1), 0, 0
			for i := 0; i < 30; i++ {
				id, b := fmt.Sprintf("r%d", i), int64(rng.Intn(1000))
				ck(t, x.Begin(id, b))
				inf++
				if i%2 == 1 { // odd ids stay in flight; 15 completions are guaranteed
					continue
				}
				l := int64(rng.Intn(30))
				ck(t, x.End(id, b+l))
				inf--
				sum, done = sum+l, done+1
				if l > T {
					viol++
				}
				mn, mx = min(mn, l), max(mx, l)
			}
			avg := float64(sum) / float64(done)
			if x.Count() != int64(done) || x.Violations() != viol || x.InFlight() != inf ||
				x.MinLatency() != mn || x.MaxLatency() != mx || x.AvgLatency() != avg {
				t.Fatalf("T=%d tr=%d mismatch", T, tr)
			}
		}
	}
}

// TestThresholdBoundary pins invariant 2: ==T OK, T+1 violates, in-flight absent.
func TestThresholdBoundary(t *testing.T) {
	lats := []int64{9, 10, 11}
	wantV := []int64{0, 0, 1}
	for i, lat := range lats {
		x, _ := New(10, 1, 1)
		if x.Begin("p", 0) != nil || x.Count() != 0 || x.InFlight() != 1 {
			t.Fatalf("lat=%d: in-flight leaked", lat)
		}
		if err := x.End("p", lat); err != nil || x.Violations() != wantV[i] {
			t.Fatalf("lat=%d: v=%d want %d err=%v", lat, x.Violations(), wantV[i], err)
		}
	}
}

// TestRejectedOpsAtomic pins invariant 4: four distinct sentinels, zero trace.
func TestRejectedOpsAtomic(t *testing.T) {
	x, _ := New(10, 4, 2)
	for _, p := range []struct {
		t    int64
		w, k int
	}{{-1, 4, 2}, {10, 0, 2}, {10, 4, 0}, {10, 4, 5}} {
		_, err := New(p.t, p.w, p.k)
		if !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("New(%+v): %v", p, err)
		}
	}
	if x.Begin("dup", 0) != nil || x.Begin("neg", 5) != nil || x.Begin("done", 0) != nil || x.End("done", 1) != nil {
		t.Fatal("fixture setup failed")
	}
	before := snap(x)
	for _, c := range []struct {
		name string
		fn   func() error
		want error
	}{
		{"dup", func() error { return x.Begin("dup", 1) }, ErrDuplicateBegin},
		{"never-began", func() error { return x.End("ghost", 9) }, ErrUnknown},
		{"already-ended", func() error { return x.End("done", 2) }, ErrUnknown},
		{"negative", func() error { return x.End("neg", 4) }, ErrNegative},
	} {
		if err := c.fn(); !errors.Is(err, c.want) || snap(x) != before {
			t.Fatalf("%s: err=%v changed=%v", c.name, err, snap(x) != before)
		}
	}
	if err := x.End("neg", 6); err != nil || x.Count() != 2 { // latency 1: still usable
		t.Fatalf("unusable: err=%v count=%d", err, x.Count())
	}
}

// TestConcurrentReads: seeded readers observe itemwise-identical snapshots.
func TestConcurrentReads(t *testing.T) {
	x, _ := New(10, 4, 3)
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("r%d", i)
		err := x.Begin(id, int64(i*10))
		if i < 12 {
			err = x.End(id, int64(i*10+11))
		}
		ck(t, err)
	}
	const n = 16
	got := make([]snapshot, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for g := 0; g < n; g++ {
		go func(g int) { got[g] = snap(x); wg.Done() }(g)
	}
	wg.Wait()
	for g := 1; g < n; g++ {
		if got[g] != got[0] {
			t.Fatalf("reader %d %+v != %+v", g, got[g], got[0])
		}
	}
}

func TestSelfCheck(t *testing.T) { ck(t, SelfCheck()) }
