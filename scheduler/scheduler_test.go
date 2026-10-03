package scheduler

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestDistanceExamples(t *testing.T) {
	cases := []struct {
		window []int
		m      int
		want   int
	}{
		{[]int{1, 1, 1}, 2, 2},
		{[]int{1, 1, 0}, 2, 1},
		{[]int{1, 0, 1}, 2, 1},
		{[]int{0, 1, 1}, 2, 2},
		{[]int{1, 0, 0}, 2, 0},
		{[]int{1, 1, 1, 1}, 4, 1},
		{[]int{1, 1, 1, 0}, 4, 0},
	}

	for _, tc := range cases {
		if got := distance(tc.window, tc.m); got != tc.want {
			t.Fatalf("distance(%v, %d) = %d, want %d", tc.window, tc.m, got, tc.want)
		}
	}
}

func TestFirstExample(t *testing.T) {
	s := NewScheduler()
	mustAdd(t, s, TaskSpec{ID: "a", Phi: 0, T: 3, C: 1, M: 1, K: 3})
	mustAdd(t, s, TaskSpec{ID: "b", Phi: 0, T: 4, C: 3, M: 2, K: 3})

	if err := s.Step(8); err != nil {
		t.Fatal(err)
	}

	if got := trace(t, s, 8); got != "bbbabbba" {
		t.Fatalf("trace = %q, want bbbabbba", got)
	}
	assertWindow(t, s, "a", []int{0, 1, 1})
	assertStats(t, s, "a", Stats{Met: 2, Missed: 1, DynamicFailures: 0})
	assertWindow(t, s, "b", []int{1, 1, 1})
}

func TestSecondExample(t *testing.T) {
	s := NewScheduler()
	mustAdd(t, s, TaskSpec{ID: "a", Phi: 0, T: 3, C: 1, M: 1, K: 2})
	mustAdd(t, s, TaskSpec{ID: "b", Phi: 0, T: 5, C: 4, M: 1, K: 2})

	if err := s.Step(8); err != nil {
		t.Fatal(err)
	}

	if got := trace(t, s, 8); got != "abbbbaa." {
		t.Fatalf("trace = %q, want abbbbaa.", got)
	}
	assertWindow(t, s, "b", []int{1, 0})
	assertStats(t, s, "b", Stats{Met: 1, Missed: 1, DynamicFailures: 0})
}

func TestDeadlineBoundariesAndTieBreaks(t *testing.T) {
	t.Run("remaining equal to slack survives and meets at deadline", func(t *testing.T) {
		s := NewScheduler()
		mustAdd(t, s, TaskSpec{ID: "late", Phi: 0, T: 4, C: 4, M: 1, K: 2})

		if err := s.Step(4); err != nil {
			t.Fatal(err)
		}
		if got := trace(t, s, 4); got != "llll" {
			t.Fatalf("trace = %q, want llll", got)
		}
		assertStats(t, s, "late", Stats{Met: 1, Missed: 0, DynamicFailures: 0})
	})

	t.Run("remaining one greater than slack is discarded before scheduling", func(t *testing.T) {
		s := NewScheduler()
		mustAdd(t, s, TaskSpec{ID: "late", Phi: 0, T: 3, C: 3, M: 2, K: 3})
		mustAdd(t, s, TaskSpec{ID: "block", Phi: 0, T: 4, C: 2, M: 2, K: 2})

		if err := s.Step(3); err != nil {
			t.Fatal(err)
		}
		if got := trace(t, s, 3); got != "bb." {
			t.Fatalf("trace = %q, want bb.", got)
		}
		if got, err := s.RunAt(3); err == nil || got != "" {
			t.Fatalf("unrecorded tick returned %q, %v", got, err)
		}
		if err := s.Step(1); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.RunAt(3); got != "late" {
			t.Fatalf("tick 3 = %q, want newly released late", got)
		}
		assertStats(t, s, "late", Stats{Met: 0, Missed: 1, DynamicFailures: 0})
		assertWindow(t, s, "late", []int{1, 1, 0})
	})

	t.Run("same distance and deadline uses bytewise id", func(t *testing.T) {
		s := NewScheduler()
		mustAdd(t, s, TaskSpec{ID: "b", Phi: 0, T: 2, C: 1, M: 1, K: 2})
		mustAdd(t, s, TaskSpec{ID: "a", Phi: 0, T: 2, C: 1, M: 1, K: 2})

		if err := s.Step(1); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.RunAt(0); got != "a" {
			t.Fatalf("tick 0 = %q, want a", got)
		}
	})
}

func TestPhiAndDynamicFailures(t *testing.T) {
	s := NewScheduler()
	mustAdd(t, s, TaskSpec{ID: "late", Phi: 2, T: 2, C: 2, M: 2, K: 2})
	mustAdd(t, s, TaskSpec{ID: "blocker", Phi: 2, T: 2, C: 2, M: 2, K: 2})

	if err := s.Step(5); err != nil {
		t.Fatal(err)
	}

	if got := trace(t, s, 5); got != "..bbl" {
		t.Fatalf("trace = %q, want ..bbl", got)
	}
	assertWindow(t, s, "late", []int{1, 0})
	assertStats(t, s, "late", Stats{Met: 0, Missed: 1, DynamicFailures: 1})
}

func TestConcurrentOperations(t *testing.T) {
	s := NewScheduler()
	mustAdd(t, s, TaskSpec{ID: "a", Phi: 0, T: 3, C: 2, M: 2, K: 3})
	mustAdd(t, s, TaskSpec{ID: "b", Phi: 1, T: 4, C: 2, M: 1, K: 3})

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if id < 2 {
				for range 20 {
					if err := s.Step(1); err != nil {
						t.Errorf("Step: %v", err)
					}
				}
				return
			}
			for range 20 {
				_, _ = s.Window("a")
				_, _ = s.Distance("b")
				_, _ = s.Stats("a")
				_, _ = s.RunAt(0)
			}
		}(worker)
	}
	wg.Wait()

	if _, err := s.RunAt(39); err != nil {
		t.Fatalf("expected 40 ticks: %v", err)
	}
	if _, err := s.RunAt(40); !errors.Is(err, ErrTickNotFound) {
		t.Fatalf("tick 40 error = %v, want ErrTickNotFound", err)
	}
}

func TestRejectionOrderAndNoStateChange(t *testing.T) {
	s := NewScheduler()
	mustAdd(t, s, TaskSpec{ID: "dup", Phi: 0, T: 1, C: 1, M: 1, K: 1})

	if err := s.AddTask(TaskSpec{ID: "", T: 0, C: 0, M: 0, K: 0}); !errors.Is(err, ErrInvalidTask) {
		t.Fatalf("invalid task error = %v, want ErrInvalidTask", err)
	}
	if err := s.AddTask(TaskSpec{ID: "dup", Phi: 0, T: 1, C: 1, M: 1, K: 1}); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("duplicate error = %v, want ErrDuplicateID", err)
	}

	for i := len(s.tasks); i < MaxTasks; i++ {
		mustAdd(t, s, TaskSpec{ID: string(rune('a' + i)), Phi: 0, T: 1, C: 1, M: 1, K: 1})
	}
	if err := s.AddTask(TaskSpec{ID: "full", Phi: 0, T: 1, C: 1, M: 1, K: 1}); !errors.Is(err, ErrTaskLimit) {
		t.Fatalf("limit error = %v, want ErrTaskLimit", err)
	}

	if err := s.Step(0); !errors.Is(err, ErrInvalidStep) {
		t.Fatalf("zero step error = %v", err)
	}
	if err := s.AddTask(TaskSpec{ID: "after", Phi: 0, T: 1, C: 1, M: 1, K: 1}); !errors.Is(err, ErrTaskLimit) {
		t.Fatalf("before first valid tick error = %v, want ErrTaskLimit", err)
	}

	if err := s.Step(1); err != nil {
		t.Fatal(err)
	}
	if err := s.AddTask(TaskSpec{ID: "valid", Phi: 0, T: 1, C: 1, M: 1, K: 1}); !errors.Is(err, ErrStarted) {
		t.Fatalf("started error = %v, want ErrStarted", err)
	}
	if err := s.AddTask(TaskSpec{ID: "", T: 0, C: 0, M: 0, K: 0}); !errors.Is(err, ErrStarted) {
		t.Fatalf("started has priority over invalid: %v", err)
	}
	if _, err := s.Window("missing"); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("window missing error = %v", err)
	}
	if _, err := s.Distance("missing"); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("distance missing error = %v", err)
	}
	if _, err := s.Stats("missing"); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("stats missing error = %v", err)
	}
}

func mustAdd(t *testing.T, s *Scheduler, spec TaskSpec) {
	t.Helper()
	if err := s.AddTask(spec); err != nil {
		t.Fatalf("AddTask(%+v): %v", spec, err)
	}
}

func trace(t *testing.T, s *Scheduler, n int64) string {
	t.Helper()
	var builder strings.Builder
	for tick := int64(0); tick < n; tick++ {
		id, err := s.RunAt(tick)
		if err != nil {
			t.Fatalf("RunAt(%d): %v", tick, err)
		}
		if id == "" {
			builder.WriteByte('.')
			continue
		}
		builder.WriteByte(id[0])
	}
	return builder.String()
}

func assertWindow(t *testing.T, s *Scheduler, id string, want []int) {
	t.Helper()
	got, err := s.Window(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("window %s = %v, want %v", id, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("window %s = %v, want %v", id, got, want)
		}
	}
}

func assertStats(t *testing.T, s *Scheduler, id string, want Stats) {
	t.Helper()
	got, err := s.Stats(id)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("stats %s = %+v, want %+v", id, got, want)
	}
}
