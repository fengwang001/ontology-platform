package api_test

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
	"ontology/bidx"
)

// TestForwardTSAt pins invariant 1 at the public boundary: TSAt(o) must
// equal exactly the o-th Append argument, including negative and
// duplicate timestamps.
func TestForwardTSAt(t *testing.T) {
	cases := []struct {
		name string
		ts   []int64
	}{
		{"spec8", []int64{2, 1, 8, 3, 4, 9, 5, 7}},
		{"neg", []int64{-9, -1, -9, 0}},
		{"dup", []int64{5, 5, 5}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			x := api.New(len(c.ts))
			for _, v := range c.ts {
				if err := x.Append(v); err != nil {
					t.Fatal(err)
				}
			}
			if x.Len() != len(c.ts) {
				t.Fatalf("Len=%d want %d", x.Len(), len(c.ts))
			}
			for o, want := range c.ts {
				got, err := x.TSAt(int64(o))
				if err != nil || got != want {
					t.Fatalf("TSAt(%d)=(%d,%v) want %d", o, got, err, want)
				}
			}
			for _, bad := range []int64{-1, int64(len(c.ts)), int64(len(c.ts) + 1)} {
				if _, err := x.TSAt(bad); !errors.Is(err, bidx.ErrOutOfRange) {
					t.Fatalf("TSAt(%d) err=%v want ErrOutOfRange", bad, err)
				}
			}
		})
	}
}

// TestRejectedOpsNoTrace pins invariant 4: every rejected operation
// fails wholesale, changes nothing, and the index stays usable.
func TestRejectedOpsNoTrace(t *testing.T) {
	x := api.New(2)
	for _, v := range []int64{3, 4} {
		if err := x.Append(v); err != nil {
			t.Fatal(err)
		}
	}
	e := api.New(8)
	cases := []struct {
		name       string
		call       func() error
		want       error
		lenX, lenE int
	}{
		{"capacity", func() error { return x.Append(5) }, api.ErrCapacity, 2, 0},
		{"tsat past end", func() error { _, err := x.TSAt(9); return err }, bidx.ErrOutOfRange, 2, 0},
		{"tsat negative", func() error { _, err := x.TSAt(-1); return err }, bidx.ErrOutOfRange, 2, 0},
		{"empty safeoff", func() error { _, _, err := e.SafeOff(0); return err }, bidx.ErrEmptyLog, 2, 0},
	}
	for _, c := range cases {
		if err := c.call(); !errors.Is(err, c.want) {
			t.Fatalf("%s: err=%v want %v", c.name, err, c.want)
		}
		if x.Len() != c.lenX || e.Len() != c.lenE {
			t.Fatalf("%s left a trace: lens=(%d,%d) want (%d,%d)", c.name, x.Len(), e.Len(), c.lenX, c.lenE)
		}
	}
	if v, err := x.TSAt(1); err != nil || v != 4 { // still usable
		t.Fatalf("full index unusable after rejection: (%d,%v)", v, err)
	}
	if err := e.Append(7); err != nil || e.Len() != 1 { // still usable
		t.Fatal("empty-log rejection poisoned the index")
	}
}

func TestSentinelErrorsDistinct(t *testing.T) {
	sentinels := []error{api.ErrCapacity, bidx.ErrOutOfRange, bidx.ErrEmptyLog}
	for i := range sentinels {
		for j := i + 1; j < len(sentinels); j++ {
			if sentinels[i] == sentinels[j] || errors.Is(sentinels[i], sentinels[j]) {
				t.Fatalf("sentinels %d and %d are not distinct", i, j)
			}
		}
	}
}

func TestSelfCheck(t *testing.T) {
	for _, n := range []int{0, 1, 8} {
		if err := api.New(n).SelfCheck(); err != nil {
			t.Fatal(err)
		}
	}
}

// TestConcurrentReaders: many goroutines read the same finished index;
// every TSAt and SafeOff value must match the same reference. Only a
// WaitGroup is used for synchronization, no sleeps.
func TestConcurrentReaders(t *testing.T) {
	const n, readers = 2000, 16
	r := rand.New(rand.NewSource(7))
	x := api.New(n)
	ts := make([]int64, n)
	for i := range ts {
		ts[i] = r.Int63n(100) - 50
		_ = x.Append(ts[i])
	}
	Ts := []int64{-100, -51, -50, -1, 0, 1, 49, 50, 100}
	wantOff, wantFound := make([]int64, len(Ts)), make([]bool, len(Ts))
	for i, T := range Ts {
		wantOff[i], wantFound[i], _ = x.SafeOff(T)
	}
	var wg sync.WaitGroup
	for g := 0; g < readers; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for o, want := range ts {
				if v, err := x.TSAt(int64(o)); err != nil || v != want {
					t.Errorf("TSAt(%d)=(%d,%v) want %d", o, v, err, want)
				}
			}
			for i, T := range Ts {
				off, found, _ := x.SafeOff(T)
				if off != wantOff[i] || found != wantFound[i] {
					t.Errorf("SafeOff(%d)=(%d,%v) want (%d,%v)", T, off, found, wantOff[i], wantFound[i])
				}
			}
		}()
	}
	wg.Wait()
}
