package api

import (
	"errors"
	"reflect"
	"sort"
	"sync"
	"testing"
)

func keyInts(ps []Pair) []int {
	o := make([]int, len(ps))
	for i, q := range ps {
		o[i] = int(q.P)
	}
	sort.Ints(o)
	return o
}

// New rejects invalid N and M with distinct sentinel errors.
func TestNewValidation(t *testing.T) {
	cases := []struct {
		n, m int
		want error
	}{
		{1, 1, ErrInvalidN},
		{0, 1, ErrInvalidN},
		{-3, 1, ErrInvalidN},
		{2, 0, ErrInvalidM},
		{2, -1, ErrInvalidM},
		{100, -5, ErrInvalidM},
	}
	for _, c := range cases {
		_, err := New(c.n, c.m)
		if !errors.Is(err, c.want) {
			t.Fatalf("New(%d,%d): got %v want %v", c.n, c.m, err, c.want)
		}
	}
	if errors.Is(ErrInvalidN, ErrInvalidM) || errors.Is(ErrInvalidM, ErrNegativeKey) {
		t.Fatal("the three sentinel errors must be distinct")
	}
}

// A rejected Build/Probe batch changes no state; the engine stays usable.
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	e, err := New(4, 2)
	if err != nil {
		t.Fatal(err)
	}
	initB := []Key{1, 5, 2, 6, 9, 3}
	p := []Key{5, 9, 3, 2, 1, 7}
	if err := e.Build(initB); err != nil {
		t.Fatal(err)
	}
	before, _ := e.Probe(p)

	// A Build batch mixing valid and negative keys is wholly rejected.
	if err := e.Build([]Key{4, 4, 4, -1}); !errors.Is(err, ErrNegativeKey) {
		t.Fatalf("negative Build: got %v want ErrNegativeKey", err)
	}
	if got, _ := e.Probe(p); !reflect.DeepEqual(keyInts(got), keyInts(before)) {
		t.Fatal("state changed after rejected Build")
	}
	// A Probe batch containing a negative key is rejected and returns nothing.
	if got, err := e.Probe([]Key{1, -9}); err == nil || !errors.Is(err, ErrNegativeKey) || got != nil {
		t.Fatalf("negative Probe: got (%v,%v), want (nil,ErrNegativeKey)", got, err)
	}
	if got, _ := e.Probe(p); !reflect.DeepEqual(keyInts(got), keyInts(before)) {
		t.Fatal("state changed after rejected Probe")
	}
	// The engine remains fully usable afterwards.
	if err := e.Build([]Key{7, 7}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.Probe([]Key{7, 8}); len(got) != 2 {
		t.Fatalf("after recovery got %d pairs, want 2", len(got))
	}
}

// Build/Probe through the public API matches naive, and SelfCheck passes.
func TestPublicJoinAndSelfCheck(t *testing.T) {
	cases := []struct{ n, m, seed int }{
		{2, 1, 1}, {4, 2, 2}, {7, 3, 3}, {16, 5, 4},
	}
	for s := 10; s < 40; s += 7 {
		cases = append(cases, struct{ n, m, seed int }{2 + s%9, 1 + s%4, s})
	}
	for _, c := range cases {
		e, _ := New(c.n, c.m)
		var b, p []Key
		for i := 0; i < c.seed*4; i++ {
			b = append(b, Key((i*7+c.seed)%19))
		}
		for i := 0; i < c.seed*3; i++ {
			p = append(p, Key((i*13+c.seed)%23))
		}
		if err := e.Build(b); err != nil {
			t.Fatal(err)
		}
		got, err := e.Probe(p)
		if err != nil || !reflect.DeepEqual(keyInts(got), naive(b, p)) {
			t.Fatalf("case %+v mismatch: got %v err %v", c, keyInts(got), err)
		}
		if err := e.SelfCheck(); err != nil {
			t.Fatalf("SelfCheck: %v", err)
		}
	}
}

// Probe and SelfCheck are safe to call from many goroutines at once.
func TestConcurrentProbeAndSelfCheck(t *testing.T) {
	e, _ := New(7, 2)
	var b, p []Key
	for i := 0; i < 400; i++ {
		b = append(b, Key(i%31))
	}
	for i := 0; i < 300; i++ {
		p = append(p, Key(i%37))
	}
	if err := e.Build(b); err != nil {
		t.Fatal(err)
	}
	want := keyInts(mustProbe(t, e, p))
	var wg sync.WaitGroup
	for g := 0; g < 24; g++ {
		wg.Add(1)
		go func(useCheck bool) {
			defer wg.Done()
			if useCheck {
				if err := e.SelfCheck(); err != nil {
					t.Errorf("SelfCheck: %v", err)
				}
				return
			}
			if got := keyInts(mustProbe(t, e, p)); !reflect.DeepEqual(got, want) {
				t.Errorf("concurrent probe mismatch")
			}
		}(g%2 == 0)
	}
	wg.Wait()
}

func mustProbe(t *testing.T, e *Engine, ks []Key) []Pair {
	t.Helper()
	ps, err := e.Probe(ks)
	if err != nil {
		t.Fatal(err)
	}
	return ps
}
