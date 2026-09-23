package align

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"testing"

	"ontology/point"
)

func TestBucketStartFloor(t *testing.T) {
	cases := []struct {
		ts, step, want int64
	}{
		{-1, 10, -10},
		{0, 10, 0},
		{9, 10, 0},
		{10, 10, 10},
		{-10, 10, -10},
		{-11, 10, -20},
		{-19, 10, -20},
		{25, 10, 20},
		{-7, 3, -9},
	}
	for _, c := range cases {
		got, err := BucketStart(c.ts, c.step)
		if err != nil || got != c.want {
			t.Fatalf("BucketStart(%d,%d)=%d,%v want %d", c.ts, c.step, got, err, c.want)
		}
	}
}

func TestInvalidStep(t *testing.T) {
	for _, step := range []int64{0, -1, -10} {
		if _, err := BucketStart(1, step); !errors.Is(err, ErrInvalidStep) {
			t.Fatalf("step %d: want ErrInvalidStep, got %v", step, err)
		}
		if _, err := New(step, 10, nil); !errors.Is(err, ErrInvalidStep) {
			t.Fatalf("new step %d: want ErrInvalidStep, got %v", step, err)
		}
	}
}

func TestBoundaryMembership(t *testing.T) {
	const step int64 = 10
	starts := []int64{-10, 0, 20}
	offsets := map[string]int64{"atStart": 0, "beforeEnd": step - 1, "atEnd": step}
	for _, s := range starts {
		for name, off := range offsets {
			got, _ := Contains(s, step, s+off)
			want := off < step
			if got != want {
				t.Fatalf("bucket %d offset %s: got %v want %v", s, name, got, want)
			}
			bs, _ := BucketStart(s+off, step)
			if bs == s+step && want {
				t.Fatalf("point at bucket %d offset %s leaked into next bucket", s, name)
			}
			if name == "atEnd" && bs != s+step {
				t.Fatalf("atEnd must belong to next bucket, got start %d", bs)
			}
		}
	}
}

func run(points []point.Point, step, max int64) []point.Bucket {
	var out []point.Bucket
	d, _ := New(step, max, func(b point.Bucket) { out = append(out, b) })
	for _, p := range points {
		_ = d.Push(p)
	}
	d.Drain()
	return out
}

func TestShuffleDeterminism(t *testing.T) {
	base := []point.Point{}
	for i := 0; i < 50; i++ {
		base = append(base, point.Point{TS: int64(i), Value: float64(i%7) + 0.5})
	}
	want := run(base, 10, 100)
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 20; iter++ {
		sh := append([]point.Point(nil), base...)
		rng.Shuffle(len(sh), func(i, j int) { sh[i], sh[j] = sh[j], sh[i] })
		got := run(sh, 10, 100)
		if len(got) != len(want) {
			t.Fatalf("iter %d len %d want %d", iter, len(got), len(want))
		}
		for i := range want {
			if got[i].Start != want[i].Start || got[i].Count != want[i].Count ||
				math.Float64bits(got[i].Value) != math.Float64bits(want[i].Value) {
				t.Fatalf("iter %d bucket %d: got %+v want %+v", iter, i, got[i], want[i])
			}
		}
	}
}

func TestWindowResources(t *testing.T) {
	var out []point.Bucket
	d, _ := New(10, 100, func(b point.Bucket) { out = append(out, b) })
	for i := int64(0); i < 1_000_000; i++ {
		if err := d.Push(point.Point{TS: i * 10, Value: float64(i)}); err != nil {
			t.Fatalf("push %d: %v", i, err)
		}
	}
	d.Drain()
	st := d.Stats()
	if st.PeakResident > 100 {
		t.Fatalf("peak resident %d exceeds 100", st.PeakResident)
	}
	if st.PointsProcessed != 1_000_000 {
		t.Fatalf("processed %d want 1000000", st.PointsProcessed)
	}
	if len(out) != 100_000 {
		t.Fatalf("buckets %d want 100000", len(out))
	}
	for i := range out {
		if out[i].Start != int64(i)*10 || out[i].Count != 10 {
			t.Fatalf("bucket %d malformed: %+v", i, out[i])
		}
	}
}

func TestLateAndNaNAndInf(t *testing.T) {
	d, _ := New(10, 2, func(point.Bucket) {})
	_ = d.Push(point.Point{TS: 0, Value: 1})
	_ = d.Push(point.Point{TS: 20, Value: 1})
	if err := d.Push(point.Point{TS: -10, Value: 1}); !errors.Is(err, ErrLateBeyondWindow) {
		t.Fatalf("late point: want ErrLateBeyondWindow, got %v", err)
	}
	before := d.Stats().PointsProcessed
	if err := d.Push(point.Point{TS: 5, Value: math.NaN()}); !errors.Is(err, ErrNaNValue) {
		t.Fatalf("nan: want ErrNaNValue, got %v", err)
	}
	st := d.Stats()
	if st.SkippedNaN != 1 || st.PointsProcessed != before {
		t.Fatalf("nan changed counters: %+v", st)
	}
	out := run([]point.Point{
		{TS: 0, Value: 1}, {TS: 1, Value: math.Inf(1)}, {TS: 2, Value: -2},
	}, 10, 10)
	if !math.IsInf(out[0].Value, 1) {
		t.Fatalf("mean with +Inf want +Inf, got %v", out[0].Value)
	}
}

func TestConcurrentMatchesSerial(t *testing.T) {
	base := make([]point.Point, 2000)
	for i := range base {
		base[i] = point.Point{TS: int64(i % 200), Value: float64(i)*0.375 + 1.25}
	}
	serial := run(base, 10, 1000)

	var mu sync.Mutex
	var par []point.Bucket
	d, _ := New(10, 1000, func(b point.Bucket) {
		mu.Lock()
		par = append(par, b)
		mu.Unlock()
	})
	var wg sync.WaitGroup
	workers := 8
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := id; i < len(base); i += workers {
				_ = d.Push(base[i])
			}
		}(w)
	}
	wg.Wait()
	d.Drain()

	sortBuckets := func(in []point.Bucket) {
		for i := 1; i < len(in); i++ {
			for j := i; j > 0 && in[j-1].Start > in[j].Start; j-- {
				in[j-1], in[j] = in[j], in[j-1]
			}
		}
	}
	sortBuckets(par)
	if len(par) != len(serial) {
		t.Fatalf("concurrent buckets %d want %d", len(par), len(serial))
	}
	for i := range serial {
		if par[i].Start != serial[i].Start || par[i].Count != serial[i].Count ||
			math.Float64bits(par[i].Value) != math.Float64bits(serial[i].Value) {
			t.Fatalf("bucket %d concurrent %+v vs serial %+v", i, par[i], serial[i])
		}
	}
}
