package admit_test

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/admit"
	"ontology/sched"
	"ontology/task"
)

func newAdmitter(limit int) *admit.Admitter {
	a := admit.New(sched.New(), limit)
	for _, id := range []string{"a", "b"} {
		if err := a.AddTenant(id, 1); err != nil {
			panic(err)
		}
	}
	return a
}

func TestErrors(t *testing.T) {
	fill := func(a *admit.Admitter) error {
		for i := 0; i < 2; i++ {
			if err := a.Enqueue(task.Task{Tenant: "a", Cost: 1}); err != nil {
				return err
			}
		}
		return a.Enqueue(task.Task{Tenant: "a", Cost: 1})
	}
	cases := []struct {
		name string
		op   func(a *admit.Admitter) error
		want error
	}{
		{"weight zero", func(a *admit.Admitter) error { return a.AddTenant("z", 0) }, sched.ErrInvalidWeight},
		{"weight negative", func(a *admit.Admitter) error { return a.AddTenant("z", -2) }, sched.ErrInvalidWeight},
		{"cost negative", func(a *admit.Admitter) error {
			return a.Enqueue(task.Task{Tenant: "a", Cost: -1})
		}, sched.ErrInvalidCost},
		{"queue full", fill, admit.ErrQueueFull},
		{"remove busy tenant", func(a *admit.Admitter) error {
			if err := a.Enqueue(task.Task{Tenant: "a", Cost: 1}); err != nil {
				return err
			}
			return a.RemoveTenant("a")
		}, sched.ErrTenantNotEmpty},
		{"unknown tenant", func(a *admit.Admitter) error {
			return a.Enqueue(task.Task{Tenant: "ghost", Cost: 1})
		}, sched.ErrUnknownTenant},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.op(newAdmitter(2)); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want errors.Is %v", err, tc.want)
			}
		})
	}
}

func TestQueueFullIsolation(t *testing.T) {
	a := newAdmitter(2)
	for i := 0; i < 2; i++ {
		if err := a.Enqueue(task.Task{Tenant: "a", Cost: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Enqueue(task.Task{Tenant: "a", Cost: 1}); !errors.Is(err, admit.ErrQueueFull) {
		t.Fatalf("tenant a: err = %v, want ErrQueueFull", err)
	}
	if err := a.Enqueue(task.Task{Tenant: "b", Cost: 1}); err != nil {
		t.Fatalf("tenant b blocked by a's backpressure: %v", err)
	}
	if tk, ok := a.Next(); !ok || tk.Tenant != "a" {
		t.Fatalf("next = %v, want a's queued task", tk.Tenant)
	}
}

func TestConcurrentExactlyOnce(t *testing.T) {
	const total, tenants = 100000, 16
	a := admit.New(sched.New(), total)
	for i := 0; i < tenants; i++ {
		if err := a.AddTenant(fmt.Sprintf("t%d", i), float64(i%3+1)); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for p := 0; p < 8; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < total/8; i++ {
				seq := uint64(p*(total/8) + i)
				tk := task.Task{Tenant: fmt.Sprintf("t%d", seq%tenants), Cost: 1, Seq: seq}
				if err := a.Enqueue(tk); err != nil {
					t.Errorf("enqueue: %v", err)
					return
				}
			}
		}(p)
	}
	seen := make([]atomic.Int32, total)
	var consumed atomic.Int64
	for c := 0; c < 8; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for consumed.Load() < total {
				tk, ok := a.Next()
				if !ok {
					runtime.Gosched()
					continue
				}
				if seen[tk.Seq].Swap(1) != 0 {
					t.Errorf("task %d consumed twice", tk.Seq)
				}
				consumed.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := consumed.Load(); got != total {
		t.Fatalf("consumed = %d, want %d", got, total)
	}
	for i := range seen {
		if seen[i].Load() != 1 {
			t.Fatalf("task %d consumed %d times", i, seen[i].Load())
		}
	}
}
