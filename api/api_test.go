package api_test

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
	"ontology/mset"
)

type op struct {
	s api.Side
	v string
	d int
}

func track(l, r map[string]int, o op) {
	if o.s == api.L {
		l[o.v] += o.d
	} else {
		r[o.v] += o.d
	}
}

// genOps builds a deterministic pseudo-random valid operation sequence.
func genOps(n, steps int) []op {
	ops := []op{}
	l, r := map[string]int{}, map[string]int{}
	for i := 0; i < steps; i++ {
		v := fmt.Sprintf("v%d", (i*7+3)%n)
		if i%11 == 0 {
			v = mset.Null
		}
		o := op{api.Side(i % 2), v, 1}
		cur := l[v]
		if o.s == api.R {
			cur = r[v]
		}
		if i%3 == 0 && cur > 0 {
			o.d = -1
		}
		track(l, r, o)
		ops = append(ops, o)
	}
	return ops
}

func batchWant(l, r map[string]int, v string) int {
	if v == mset.Null {
		return 0
	}
	return min(l[v], r[v])
}

// runTracked applies genOps while shadow-tracking l/r and the changelog
// state m. With perPrefix it also validates every changelog prefix
// (invariant 2); it always validates the final View (invariant 1).
func runTracked(t *testing.T, n, steps int, perPrefix bool) {
	t.Helper()
	eng := api.New()
	l, r, m := map[string]int{}, map[string]int{}, map[string]int{}
	for i, o := range genOps(n, steps) {
		cs, err := eng.Apply(o.s, o.v, o.d)
		if err != nil {
			t.Fatal(err)
		}
		track(l, r, o)
		for _, c := range cs {
			if c.Delta == -1 && m[c.Val] <= 0 {
				t.Fatalf("op %d: -(%s) with no copy present", i, c.Val)
			}
			m[c.Val] += c.Delta
		}
		if perPrefix {
			for v, mv := range m {
				if mv < 0 || mv != batchWant(l, r, v) {
					t.Fatalf("prefix %d: m[%q]=%d want %d", i, v, mv, batchWant(l, r, v))
				}
			}
		}
	}
	for v := range l {
		if got := eng.View()[v]; got != batchWant(l, r, v) {
			t.Fatalf("view[%q]=%d want %d", v, got, batchWant(l, r, v))
		}
	}
	if eng.View()[mset.Null] != 0 {
		t.Fatal("null multiplicity must be 0")
	}
}

// Invariant 1: View equals batch min(l, r) per value; NULL is always 0.
func TestViewMatchesBatch(t *testing.T) {
	for _, n := range []int{5, 50} {
		runTracked(t, n, 400, false)
	}
}

// Invariant 2: every changelog prefix keeps each value at min(l, r) >= 0,
// and every -(val) retracts exactly one existing copy.
func TestChangelogPrefix(t *testing.T) { runTracked(t, 9, 300, true) }

// Invariant 4: three distinct sentinel errors; rejection leaves no trace
// and the engine keeps working afterwards.
func TestRejectedNoEffect(t *testing.T) {
	eng := api.New()
	eng.Apply(api.L, "x", 1)
	before := eng.View()
	bad := []op{{api.L, "x", 0}, {api.R, "x", -1}, {api.L, "", 1}}
	wants := []error{api.ErrZeroDelta, api.ErrNegativeCount, api.ErrEmptyVal}
	for i, o := range bad {
		if _, err := eng.Apply(o.s, o.v, o.d); !errors.Is(err, wants[i]) {
			t.Fatalf("case %d: err=%v, want %v", i, err, wants[i])
		}
	}
	if api.ErrZeroDelta == api.ErrNegativeCount || api.ErrNegativeCount == api.ErrEmptyVal || api.ErrZeroDelta == api.ErrEmptyVal {
		t.Fatal("sentinel errors not distinct")
	}
	if !reflect.DeepEqual(before, eng.View()) {
		t.Fatal("rejected ops changed the view")
	}
	if cs, err := eng.Apply(api.R, "x", 1); err != nil || len(cs) != 1 {
		t.Fatal("engine unusable after rejection")
	}
}

// SelfCheck must pass and be safe to call from many goroutines at once.
func TestSelfCheck(t *testing.T) {
	eng := api.New()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := eng.SelfCheck(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}
