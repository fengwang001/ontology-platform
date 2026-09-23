package align

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"testing"

	"ontology/agg"
	"ontology/point"
)

func TestBucketStart(t *testing.T) {
	cases := []struct {
		ts, step, want int64
	}{
		{-1, 10, -10}, {-10, 10, -10}, {-11, 10, -20}, {0, 10, 0},
		{1, 10, 0}, {9, 10, 0}, {10, 10, 10}, {-99, 10, -100},
		{25, 7, 21}, {-1, 7, -7}, {-7, 7, -7},
	}
	for _, tc := range cases {
		got, err := BucketStart(tc.ts, tc.step)
		if err != nil || got != tc.want {
			t.Fatalf("BucketStart(%d,%d)=%d,%v want %d", tc.ts, tc.step, got, err, tc.want)
		}
	}
	for _, bad := range []int64{0, -1, -10} {
		if _, err := BucketStart(1, bad); !errors.Is(err, ErrInvalidStep) {
			t.Fatalf("step %d should be ErrInvalidStep, got %v", bad, err)
		}
	}
}

func TestBoundaries(t *testing.T) {
	const step int64 = 10
	starts := []int64{-20, -10, 0, 10, 20}
	for _, start := range starts {
		positions := []struct {
			ts   int64
			want bool
		}{
			{start, true}, {start + step - 1, true}, {start + step, false},
		}
		bs, err := BucketStart(start, step)
		if err != nil || bs != start {
			t.Fatalf("anchor %d: got %d %v", start, bs, err)
		}
		for _, p := range positions {
			if got := Contains(start, step, p.ts); got != p.want {
				t.Fatalf("Contains(%d,%d,%d)=%v want %v", start, step, p.ts, got, p.want)
			}
			owner, _ := BucketStart(p.ts, step)
			if p.want && owner != start {
				t.Fatalf("ts %d owned by %d not %d", p.ts, owner, start)
			}
			if !p.want && owner == start {
				t.Fatalf("ts %d must not be owned by %d", p.ts, start)
			}
		}
	}
}

func runAll(t *testing.T, w *Window, ps []point.Point) []agg.Bucket {
	t.Helper()
	var out []agg.Bucket
	for _, p := range ps {
		bs, err := w.Push(p)
		if err != nil {
			t.Fatalf("push %+v: %v", p, err)
		}
		out = append(out, bs...)
	}
	return append(out, w.Close()...)
}

func TestShuffleDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	base := make([]point.Point, 0, 60)
	for i := 0; i < 60; i++ {
		base = append(base, point.Point{TS: int64(rng.Intn(40) * 10), Value: rng.Float64()*20 - 10})
	}
	ref := runAll(t, mustWindow(t, 10, 1000), sorted(base))
	for iter := 0; iter < 20; iter++ {
		sh := append([]point.Point(nil), base...)
		rng.Shuffle(len(sh), func(i, j int) { sh[i], sh[j] = sh[j], sh[i] })
		got := runAll(t, mustWindow(t, 10, 1000), sh)
		if len(got) != len(ref) {
			t.Fatalf("iter %d: %d buckets want %d", iter, len(got), len(ref))
		}
		for i := range ref {
			if got[i].Start != ref[i].Start ||
				math.Float64bits(got[i].First) != math.Float64bits(ref[i].First) ||
				math.Float64bits(got[i].Last) != math.Float64bits(ref[i].Last) ||
				math.Float64bits(got[i].Min) != math.Float64bits(ref[i].Min) ||
				math.Float64bits(got[i].Max) != math.Float64bits(ref[i].Max) ||
				math.Float64bits(got[i].Mean) != math.Float64bits(ref[i].Mean) ||
				got[i].Count != ref[i].Count {
				t.Fatalf("iter %d bucket %d: got %+v want %+v", iter, i, got[i], ref[i])
			}
		}
	}
}

func sorted(ps []point.Point) []point.Point {
	out := append([]point.Point(nil), ps...)
	sortPoints(out)
	return out
}

func mustWindow(t *testing.T, step int64, max int) *Window {
	t.Helper()
	w, err := NewWindow(step, max)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestWindowCounters(t *testing.T) {
	w := mustWindow(t, 10, 100)
	pts := make([]point.Point, 100_001)
	for i := range pts {
		pts[i] = point.Point{TS: int64(i * 10), Value: float64(i)}
	}
	var n int
	for _, p := range pts {
		bs, err := w.Push(p)
		if err != nil {
			t.Fatal(err)
		}
		n += len(bs)
	}
	n += len(w.Close())
	if w.Processed() != int64(len(pts)) {
		t.Fatalf("processed=%d want %d", w.Processed(), len(pts))
	}
	if w.PeakResident() > 100 {
		t.Fatalf("peak resident = %d > 100", w.PeakResident())
	}
	if n != 100_001 {
		t.Fatalf("emitted buckets = %d want 100001", n)
	}

	w2 := mustWindow(t, 10, 5)
	for _, p := range []point.Point{{TS: 0, Value: 1}, {TS: 100, Value: 2}} {
		if _, err := w2.Push(p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w2.Push(point.Point{TS: 10, Value: 9}); !errors.Is(err, ErrLate) {
		t.Fatalf("late point: got %v want ErrLate", err)
	}
	if w2.Late() != 1 {
		t.Fatalf("late count = %d", w2.Late())
	}
	if _, err := w2.Push(point.Point{TS: 50, Value: math.NaN()}); !errors.Is(err, point.ErrNaN) {
		t.Fatalf("nan: got %v want point.ErrNaN", err)
	}
	if w2.Skipped() != 1 {
		t.Fatalf("skipped = %d", w2.Skipped())
	}
	if w2.Processed() != 2 {
		t.Fatalf("processed = %d want 2", w2.Processed())
	}
}

func TestConcurrentPush(t *testing.T) {
	const workers, per = 8, 200
	var wg sync.WaitGroup
	makePoints := func() []point.Point {
		ps := make([]point.Point, per)
		for i := range ps {
			ps[i] = point.Point{TS: int64(i%50) * 10, Value: float64(i)}
		}
		return ps
	}
	all := makePoints()
	ref := runAll(t, mustWindow(t, 10, 500), sorted(concat(all, workers)))

	w := mustWindow(t, 10, 500)
	var mu sync.Mutex
	var emitted []agg.Bucket
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			ps := makePoints()
			rng.Shuffle(len(ps), func(i, j int) { ps[i], ps[j] = ps[j], ps[i] })
			for _, p := range ps {
				bs, err := w.Push(p)
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				emitted = append(emitted, bs...)
				mu.Unlock()
			}
		}(int64(g + 1))
	}
	wg.Wait()
	emitted = append(emitted, w.Close()...)
	sortBuckets(emitted)
	if len(emitted) != len(ref) {
		t.Fatalf("buckets %d want %d", len(emitted), len(ref))
	}
	for i := range ref {
		if math.Float64bits(emitted[i].Mean) != math.Float64bits(ref[i].Mean) ||
			emitted[i].Count != ref[i].Count || emitted[i].Start != ref[i].Start {
			t.Fatalf("bucket %d: %+v want %+v", i, emitted[i], ref[i])
		}
	}
	if w.Processed() != workers*per {
		t.Fatalf("processed = %d", w.Processed())
	}
}
