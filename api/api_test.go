package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
)

func clone(m map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// TestReferenceEquivalence pins invariant 1: over randomized sequences
// (keys beyond maxKeys force some rejections) Recover equals the last naive
// whole-map deep copy, and View always tracks the live reference model.
func TestReferenceEquivalence(t *testing.T) {
	for seed := int64(0); seed < 30; seed++ {
		r := rand.New(rand.NewSource(seed))
		x := api.New(6)
		model, last := map[string]int64{}, map[string]int64{}
		for step := 0; step < 40; step++ {
			k := fmt.Sprintf("k%d", r.Intn(7)) // cap 6: some Sets must reject
			if r.Intn(5) == 0 {
				x.Delete(k)
				delete(model, k)
			} else {
				v, before := int64(r.Intn(5)), clone(model)
				if err := x.Set(k, v); err == nil {
					model[k] = v
				} else if got := x.View(); !reflect.DeepEqual(got, before) {
					t.Fatalf("seed %d: rejected Set mutated state", seed)
				}
			}
			if r.Intn(6) == 0 {
				must(t, x.Checkpoint())
				last = clone(model)
			}
		}
		must(t, x.Checkpoint())
		last = clone(model)
		got, err := x.Recover()
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, last) || !reflect.DeepEqual(x.View(), model) {
			t.Fatalf("seed %d: recover=%v want=%v view=%v model=%v", seed, got, last, x.View(), model)
		}
	}
}

// TestRejectedOpsLeaveNoTrace pins invariant 4 on every rejection path:
// state is unchanged afterwards and the instance stays fully usable.
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	cases := []struct {
		name   string
		reject func(*api.API) error
	}{
		{"empty", func(x *api.API) error { return x.Set("", 1) }},
		{"overflow", func(x *api.API) error { return x.Set("z", 1) }},
		{"no-base", func(x *api.API) error { _, e := x.Recover(); return e }},
	}
	for _, c := range cases {
		x := api.New(2)
		must(t, x.Set("a", 1))
		must(t, x.Set("b", 2))
		before := x.View()
		if err := c.reject(x); err == nil {
			t.Fatalf("%s: expected rejection", c.name)
		}
		if after := x.View(); !reflect.DeepEqual(before, after) {
			t.Fatalf("%s: state changed: %v -> %v", c.name, before, after)
		}
		if err := x.Set("a", 9); err != nil || x.View()["a"] != 9 {
			t.Fatalf("%s: instance unusable after rejection", c.name)
		}
	}
}

// TestSentinelErrors pins the three distinct, errors.Is-decidable sentinels.
func TestSentinelErrors(t *testing.T) {
	x := api.New(1)
	if err := x.Set("", 1); !errors.Is(err, api.ErrEmptyKey) {
		t.Fatalf("empty key: %v", err)
	}
	must(t, x.Set("a", 1))
	if err := x.Set("b", 1); !errors.Is(err, api.ErrTooManyKeys) {
		t.Fatalf("overflow: %v", err)
	}
	if _, err := x.Recover(); !errors.Is(err, api.ErrNoBase) {
		t.Fatalf("no base: %v", err)
	}
	errs := []error{api.ErrEmptyKey, api.ErrTooManyKeys, api.ErrNoBase}
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errors.Is(errs[i], errs[j]) {
				t.Fatalf("sentinels %d and %d not distinct", i, j)
			}
		}
	}
}

// TestConcurrentReaders pins section 6: N goroutines Recover the same
// checkpointed instance behind a channel barrier (no sleeps) and agree.
func TestConcurrentReaders(t *testing.T) {
	x := api.New(64)
	for i := 0; i < 32; i++ {
		must(t, x.Set(fmt.Sprintf("k%02d", i), int64(i)))
	}
	must(t, x.Checkpoint())
	const n = 32
	got := make([]map[string]int64, n)
	start, wg := make(chan struct{}), sync.WaitGroup{}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			m, err := x.Recover()
			if err != nil {
				t.Error(err)
				return
			}
			got[i] = m
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 1; i < n; i++ {
		if !reflect.DeepEqual(got[0], got[i]) {
			t.Fatalf("reader %d disagrees", i)
		}
	}
}
