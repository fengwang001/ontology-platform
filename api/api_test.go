package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
)

func extreme(model []int64, min bool) int { // index of min (or max) value
	k := 0
	for i, v := range model {
		if min && v < model[k] || !min && v > model[k] {
			k = i
		}
	}
	return k
}

func TestNaiveConsistency(t *testing.T) { // random sequences vs naive scan reference
	for _, seed := range []int64{7, 8, 9} {
		t.Run(fmt.Sprint("seed", seed), func(t *testing.T) {
			r := rand.New(rand.NewSource(seed))
			q := api.New()
			var model []int64
			for step := 0; step < 2000; step++ {
				switch r.Intn(5) {
				case 0, 1:
					v := int64(r.Intn(201) - 100)
					q.Push(v)
					model = append(model, v)
				case 2, 3: // alternate DeleteMin/DeleteMax
					min := step%2 == 0
					var got, want int64
					var ok, wok bool
					if min {
						got, ok = q.DeleteMin()
					} else {
						got, ok = q.DeleteMax()
					}
					if len(model) > 0 {
						k := extreme(model, min)
						want, wok = model[k], true
						model = append(model[:k:k], model[k+1:]...)
					}
					if got != want || ok != wok {
						t.Fatalf("step %d: delete=(%d,%v), want (%d,%v)", step, got, ok, want, wok)
					}
				case 4:
					mn, ok1 := q.Min()
					mx, ok2 := q.Max()
					wmn, wmx, wok := int64(0), int64(0), len(model) > 0
					if wok {
						wmn, wmx = model[extreme(model, true)], model[extreme(model, false)]
					}
					if mn != wmn || mx != wmx || ok1 != wok || ok2 != wok {
						t.Fatalf("step %d: Min/Max=(%d,%d,%v), want (%d,%d,%v)", step, mn, mx, ok1, wmn, wmx, wok)
					}
				}
			}
		})
	}
}

// Empty queue: all four ops return ok=false, Len stays 0; singleton Min==Max.
func TestEmptyBoundaries(t *testing.T) {
	ops := []struct {
		name string
		call func(*api.PQ) (int64, bool)
	}{
		{"Min", (*api.PQ).Min}, {"Max", (*api.PQ).Max},
		{"DeleteMin", (*api.PQ).DeleteMin}, {"DeleteMax", (*api.PQ).DeleteMax},
	}
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			q := api.New()
			if v, ok := op.call(q); ok || v != 0 || q.Len() != 0 {
				t.Fatalf("empty %s = (%d,%v), Len=%d; want (0,false), Len=0", op.name, v, ok, q.Len())
			}
			q.Push(42)
			if v, ok := op.call(q); !ok || v != 42 {
				t.Fatalf("singleton %s = (%d,%v), want (42,true)", op.name, v, ok)
			}
		})
	}
}
func TestConservation(t *testing.T) { // Push +1, Delete -1, failed delete no-op
	q := api.New()
	steps := []struct {
		call  func()
		delta int
	}{
		{func() { q.Push(5) }, 1}, {func() { q.Push(9) }, 1}, {func() { q.DeleteMin() }, -1},
		{func() { q.Push(-3) }, 1}, {func() { q.DeleteMax() }, -1}, {func() { q.DeleteMin() }, -1},
		{func() { q.DeleteMin() }, 0}, {func() { q.DeleteMax() }, 0},
	}
	want := 0
	for i, s := range steps {
		s.call()
		want += s.delta
		if q.Len() != want {
			t.Fatalf("step %d: Len=%d, want %d", i, q.Len(), want)
		}
	}
}

func TestConcurrentPush(t *testing.T) { // N push distinct, then N drain; no sleeps
	const n = 512
	q := api.New()
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(v int64) { defer wg.Done(); q.Push(v) }(int64(i))
	}
	wg.Wait()
	mn, ok1 := q.Min()
	mx, ok2 := q.Max()
	if q.Len() != n || mn != 0 || mx != n-1 || !ok1 || !ok2 {
		t.Fatalf("Len=%d Min=(%d,%v) Max=(%d,%v), want %d/0/%d", q.Len(), mn, ok1, mx, ok2, n, n-1)
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); q.DeleteMin(); q.Min(); q.Max(); q.Len() }()
	}
	wg.Wait()
	if q.Len() != 0 {
		t.Fatalf("Len=%d after draining, want 0", q.Len())
	}
}

func TestSelfCheck(t *testing.T) { // passes; sentinel error; concurrent-safe
	if err := api.New().SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
	if !errors.Is(fmt.Errorf("wrap: %w", api.ErrSelfCheck), api.ErrSelfCheck) {
		t.Fatal("ErrSelfCheck is not matchable with errors.Is")
	}
	p := api.New()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _ = p.SelfCheck() }()
		go func(v int64) { defer wg.Done(); p.Push(v) }(int64(i))
	}
	wg.Wait()
}
