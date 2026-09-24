package admit

import (
	"errors"
	"testing"

	"ontology/sched"
	"ontology/task"
)

func setup(t *testing.T, limit int) (*sched.Scheduler, *Limiter) {
	t.Helper()
	s := sched.New()
	for _, id := range []string{"a", "b"} {
		if err := s.AddTenant(id, 1); err != nil {
			t.Fatal(err)
		}
	}
	return s, New(s, limit)
}

func TestBackpressure(t *testing.T) {
	cases := []struct {
		name  string
		limit int
	}{
		{"limit 1", 1},
		{"limit 4", 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, l := setup(t, tc.limit)
			for i := 0; i < tc.limit; i++ {
				if err := l.Submit(task.Task{Tenant: "a", Cost: 1}); err != nil {
					t.Fatalf("submit %d within limit: %v", i, err)
				}
			}
			err := l.Submit(task.Task{Tenant: "a", Cost: 1})
			if !errors.Is(err, ErrQueueFull) {
				t.Fatalf("over-limit submit: got %v, want ErrQueueFull", err)
			}
			if errors.Is(err, sched.ErrInvalidWeight) || errors.Is(err, sched.ErrEmpty) {
				t.Fatalf("ErrQueueFull not distinguishable: %v", err)
			}
			if err := l.Submit(task.Task{Tenant: "b", Cost: 1}); err != nil {
				t.Fatalf("other tenant blocked: %v", err)
			}
			if _, err := s.Next(); err != nil {
				t.Fatal(err)
			}
			if err := l.Submit(task.Task{Tenant: "a", Cost: 1}); err != nil {
				t.Fatalf("submit after dequeue: %v", err)
			}
		})
	}
}
