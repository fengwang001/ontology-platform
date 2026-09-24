package api_test

import (
	"errors"
	"math"
	"math/rand"
	"testing"

	"ontology/api"
	"ontology/wrap"
)

const th = 1<<31 - 1

func naive(seq []uint32, t uint32) []int64 {
	var prev uint32
	var pu int64
	has := false
	out := make([]int64, len(seq))
	for i, r := range seq {
		if has {
			d := uint64(r) - uint64(prev)
			if r < prev {
				if uint64(prev)-uint64(r) <= uint64(t) { // regression: no change
					out[i] = pu
					continue
				}
				d = wrap.Size - uint64(prev) + uint64(r)
			}
			pu, prev = pu+int64(d), r
		} else {
			pu, prev, has = int64(r), r, true
		}
		out[i] = pu
	}
	return out
}
func randSeq(seed int64, n int) []uint32 {
	rng := rand.New(rand.NewSource(seed))
	s := make([]uint32, n)
	for i := range s {
		s[i] = rng.Uint32()
	}
	return s
}
func TestNaiveReference(t *testing.T) {
	fixed := []uint32{100, 200, 4294967200, 50, 60, 40, 4294967295, 5}
	evWant := []api.Event{api.First, api.Forward, api.Forward, api.Wrap, api.Forward, 0, api.Forward, api.Wrap}
	want := naive(fixed, th)
	x, _ := api.New(th)
	for i, r := range fixed {
		ev, err := x.Feed(r)
		if got, _ := x.LastUnwrapped(); got != want[i] {
			t.Fatalf("fixed %d: %d!=%d", i, got, want[i])
		}
		if i == 5 {
			if !errors.Is(err, api.ErrRegression) {
				t.Fatalf("step5 want regression, got %v", err)
			}
		} else if ev != evWant[i] {
			t.Fatalf("step %d event %d!=%d", i, ev, evWant[i])
		}
	}
	for _, seed := range []int64{11, 22, 33} {
		want := naive(randSeq(seed, 1500), th)
		y, _ := api.New(th)
		for i, r := range randSeq(seed, 1500) {
			y.Feed(r)
			if got, _ := y.LastUnwrapped(); got != want[i] {
				t.Fatalf("seed %d step %d: %d!=%d", seed, i, got, want[i])
			}
		}
	}
}
func TestMonotone(t *testing.T) {
	for _, seed := range []int64{1, 2, 3} {
		x, _ := api.New(1 << 20)
		var last int64
		for i, r := range randSeq(seed, 2000) {
			ev, _ := x.Feed(r)
			got, _ := x.LastUnwrapped()
			if got < last || (i > 0 && (ev == api.Duplicate) != (got == last)) {
				t.Fatalf("seed %d monotonicity at %d ev=%d %d->%d", seed, i, ev, last, got)
			}
			last = got
		}
	}
}
func TestWrapFormula(t *testing.T) {
	for _, c := range []struct {
		pu      int64
		prev, r uint32
		th      uint32
		want    int64
	}{{4294967200, 4294967200, 50, th, 4294967346}, {8589934591, 4294967295, 5, th, 8589934597}, {0, 1<<31 + 10, 5, 1<<31 - 1, 1<<31 - 5}} {
		ev, err := wrap.Classify(c.prev, c.r, true, c.th)
		got, uerr := wrap.Unwrap(c.pu, c.prev, c.r, ev)
		if err != nil || uerr != nil || ev != wrap.Wrap || got != c.want {
			t.Fatalf("%+v ev=%d got=%d errs=%v,%v want=%d", c, ev, got, err, uerr, c.want)
		}
	}
}
func TestSentinelErrors(t *testing.T) {
	if errors.Is(api.ErrInvalidThreshold, api.ErrRegression) || errors.Is(api.ErrInvalidThreshold, api.ErrEmpty) ||
		errors.Is(api.ErrInvalidThreshold, api.ErrOverflow) || errors.Is(api.ErrRegression, api.ErrEmpty) ||
		errors.Is(api.ErrRegression, api.ErrOverflow) || errors.Is(api.ErrEmpty, api.ErrOverflow) {
		t.Fatal("sentinels are not distinct")
	}
	mk := func() *api.Tracker { z, _ := api.New(th); return z }
	for _, c := range []struct {
		name string
		run  func() error
		want error
	}{
		{"zero threshold", func() error { _, e := api.New(0); return e }, api.ErrInvalidThreshold},
		{"2^31 threshold", func() error { _, e := api.New(1 << 31); return e }, api.ErrInvalidThreshold},
		{"empty unwrapped", func() error { _, e := mk().LastUnwrapped(); return e }, api.ErrEmpty},
		{"empty raw", func() error { _, e := mk().LastRaw(); return e }, api.ErrEmpty},
		{"regression", func() error { z := mk(); z.Feed(100); _, e := z.Feed(50); return e }, api.ErrRegression},
	} {
		if !errors.Is(c.run(), c.want) {
			t.Fatalf("%s: want %v", c.name, c.want)
		}
	}
	z := mk()
	z.Feed(100)
	if _, e := z.Feed(50); !errors.Is(e, api.ErrRegression) {
		t.Fatal(e)
	}
	if u, _ := z.LastUnwrapped(); u != 100 {
		t.Fatalf("state traced: %d", u)
	}
	if ev, e := z.Feed(200); e != nil || ev != api.Forward {
		t.Fatalf("unusable after rejection: %d %v", ev, e)
	}
}
func TestOverflowRejected(t *testing.T) {
	for _, ev := range []api.Event{api.Forward, api.Wrap} {
		if _, e := wrap.Unwrap(math.MaxInt64-10, 0, 11, ev); !errors.Is(e, api.ErrOverflow) {
			t.Fatalf("ev=%d: want overflow, got %v", ev, e)
		}
	}
}
func TestSelfCheck(t *testing.T) {
	z, _ := api.New(th)
	if err := z.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
