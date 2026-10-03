package mcsched

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestSpecExamples(t *testing.T) {
	t.Run("overrun switches at end of tick and discards released LO", func(t *testing.T) {
		monitor := NewMonitor()
		mustAdd(t, monitor, Task{ID: "h", Crit: HI, CL: 2, CH: 5, T: 10, Prio: 1})
		mustAdd(t, monitor, Task{ID: "l", Crit: LO, CL: 2, CH: 2, T: 4, Prio: 2})
		if err := monitor.SetDemand("h", 0, 5); err != nil {
			t.Fatalf("SetDemand: %v", err)
		}
		if err := monitor.Step(12); err != nil {
			t.Fatalf("Step: %v", err)
		}

		want := "hhhhh...llhh"
		if got := trace(monitor, 12); got != want {
			t.Fatalf("trace = %q, want %q; switch happens after the second h tick", got, want)
		}
		stats := monitor.Stats()
		if stats.ModeSwitches != 1 || stats.ModeRecoveries != 1 || stats.DiscardedLO != 1 ||
			stats.SkippedLO != 1 || stats.Completed != 3 {
			t.Fatalf("stats = %+v", stats)
		}
		if mode := monitor.Mode(); mode != LO {
			t.Fatalf("mode = %v, want LO", mode)
		}
	})

	t.Run("demand exactly CL completes without switch", func(t *testing.T) {
		monitor := exampleMonitor(t)
		if err := monitor.SetDemand("h", 0, 2); err != nil {
			t.Fatalf("SetDemand: %v", err)
		}
		if err := monitor.Step(12); err != nil {
			t.Fatalf("Step: %v", err)
		}
		want := "hhllll..llhh"
		if got := trace(monitor, 12); got != want {
			t.Fatalf("trace = %q, want %q", got, want)
		}
		if got := monitor.Stats().ModeSwitches; got != 0 {
			t.Fatalf("switches = %d, want 0", got)
		}
	})

	t.Run("completion before threshold restores before same-time release", func(t *testing.T) {
		monitor := NewMonitor()
		mustAdd(t, monitor, Task{ID: "h", Crit: HI, CL: 2, CH: 4, T: 10, Prio: 1})
		mustAdd(t, monitor, Task{ID: "l", Crit: LO, CL: 2, CH: 2, T: 4, Prio: 2})
		if err := monitor.SetDemand("h", 0, 4); err != nil {
			t.Fatalf("SetDemand: %v", err)
		}
		if err := monitor.Step(12); err != nil {
			t.Fatalf("Step: %v", err)
		}
		want := "hhhhll..llhh"
		if got := trace(monitor, 12); got != want {
			t.Fatalf("trace = %q, want %q; at t=4 recovery must precede release", got, want)
		}
	})
}

func TestModeTransitionEdges(t *testing.T) {
	t.Run("one beyond CL switches", func(t *testing.T) {
		monitor := NewMonitor()
		mustAdd(t, monitor, Task{ID: "h", Crit: HI, CL: 2, CH: 3, T: 10, Prio: 1})
		if err := monitor.SetDemand("h", 0, 3); err != nil {
			t.Fatal(err)
		}
		mustStep(t, monitor, 2)
		if mode := monitor.Mode(); mode != HI {
			t.Fatalf("mode after tick 2 = %v, want HI", mode)
		}
		if got := monitor.Stats().ModeSwitches; got != 1 {
			t.Fatalf("switches = %d", got)
		}
	})

	t.Run("already HI HI job does not switch again", func(t *testing.T) {
		monitor := NewMonitor()
		mustAdd(t, monitor, Task{ID: "h1", Crit: HI, CL: 1, CH: 4, T: 10, Prio: 1})
		mustAdd(t, monitor, Task{ID: "h2", Crit: HI, CL: 2, CH: 3, T: 10, Prio: 2})
		mustSet(t, monitor, "h1", 0, 2)
		mustSet(t, monitor, "h2", 0, 3)
		mustStep(t, monitor, 5)
		if got := monitor.Stats().ModeSwitches; got != 1 {
			t.Fatalf("switches = %d, want exactly 1", got)
		}
	})

	t.Run("deadline miss is counted before idle recovery", func(t *testing.T) {
		monitor := NewMonitor()
		mustAdd(t, monitor, Task{ID: "x", Crit: HI, CL: 1, CH: 1, T: 10, Prio: 1})
		mustAdd(t, monitor, Task{ID: "h", Crit: HI, CL: 1, CH: 4, T: 4, Prio: 2})
		mustSet(t, monitor, "h", 0, 4)
		mustStep(t, monitor, 5)
		stats := monitor.Stats()
		if stats.ModeSwitches != 1 || stats.MissedHI != 1 || stats.ModeRecoveries != 1 {
			t.Fatalf("stats = %+v", stats)
		}
	})
}

func TestSkippedJobKeepsIndexAndDemand(t *testing.T) {
	monitor := NewMonitor()
	mustAdd(t, monitor, Task{ID: "h", Crit: HI, CL: 1, CH: 3, T: 10, Prio: 1})
	mustAdd(t, monitor, Task{ID: "l", Crit: LO, CL: 2, CH: 2, T: 2, Prio: 2})
	mustSet(t, monitor, "h", 0, 3)
	mustSet(t, monitor, "l", 1, 1)
	mustStep(t, monitor, 3)

	if got := monitor.Stats().SkippedLO; got != 1 {
		t.Fatalf("skipped = %d, want 1", got)
	}
	mustStep(t, monitor, 3)
	if got := trace(monitor, 6); got != "hhh.ll" {
		t.Fatalf("trace = %q; skipped job 0 must still occupy its index", got)
	}

	if err := monitor.SetDemand("l", 0, 1); !errors.Is(err, ErrJobReleased) {
		t.Fatalf("SetDemand skipped released job = %v, want ErrJobReleased", err)
	}

	releasedNow := NewMonitor()
	mustAdd(t, releasedNow, Task{ID: "h", Crit: HI, CL: 1, CH: 2, T: 2, Prio: 1, Phi: 1})
	if err := releasedNow.SetDemand("h", 0, 2); err != nil {
		t.Fatalf("SetDemand at release time: %v", err)
	}
}

func TestValidationOrderAndAtomicRejection(t *testing.T) {
	monitor := exampleMonitor(t)
	badID := string(make([]byte, 33))
	duplicateID := Task{ID: "h", Crit: HI, CL: 2, CH: 1, T: 2, Prio: 3}
	duplicatePrio := Task{ID: "x", Crit: HI, CL: 1, CH: 1, T: 2, Prio: 1}
	full := Task{ID: badID, Crit: HI, CL: 1, CH: 1, T: 2, Prio: 3}
	if err := monitor.AddTask(duplicateID); !errors.Is(err, ErrInvalidTask) {
		t.Fatalf("invalid before duplicate id: %v", err)
	}
	if err := monitor.AddTask(Task{ID: "h", Crit: HI, CL: 1, CH: 1, T: 2, Prio: 3}); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("duplicate id: %v", err)
	}
	if err := monitor.AddTask(duplicatePrio); !errors.Is(err, ErrDuplicatePrio) {
		t.Fatalf("duplicate priority: %v", err)
	}
	if err := monitor.AddTask(full); !errors.Is(err, ErrInvalidTask) {
		t.Fatalf("validation before capacity: %v", err)
	}

	mustStep(t, monitor, 1)
	task := Task{ID: "z", Crit: HI, CL: 1, CH: 1, T: 1, Prio: 3}
	if err := monitor.AddTask(task); !errors.Is(err, ErrStarted) {
		t.Fatalf("started check: %v", err)
	}

	before := monitor.Stats()
	for _, err := range []error{
		monitor.SetDemand("missing", 0, 1),
		monitor.SetDemand("h", -1, 1),
		monitor.SetDemand("h", 0, 6),
		monitor.SetDemand("h", 0, 1),
		monitor.Step(0),
		monitor.Step(1_000_001),
	} {
		if err == nil {
			t.Fatal("expected rejection")
		}
	}
	if got := monitor.Stats(); got != before {
		t.Fatalf("stats changed after rejection: before %+v, after %+v", before, got)
	}
}

func TestStepSplitEquivalenceAndConcurrency(t *testing.T) {
	first := randomScenarioMonitor(t, 19)
	if err := first.Step(37); err != nil {
		t.Fatal(err)
	}
	second := randomScenarioMonitor(t, 19)
	for _, ticks := range []int{1, 1, 2, 5, 8, 20} {
		if err := second.Step(ticks); err != nil {
			t.Fatal(err)
		}
	}
	if trace(first, 37) != trace(second, 37) || first.Stats() != second.Stats() {
		t.Fatalf("split steps differ:\n%s\n%s", trace(first, 37), trace(second, 37))
	}

	var wait sync.WaitGroup
	for range 16 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_ = first.Mode()
			_ = first.Stats()
			_, _ = first.RunAt(10)
			_ = first.CurrentTime()
		}()
	}
	wait.Wait()
}

func exampleMonitor(t *testing.T) *Monitor {
	t.Helper()
	monitor := NewMonitor()
	mustAdd(t, monitor, Task{ID: "h", Crit: HI, CL: 2, CH: 5, T: 10, Prio: 1})
	mustAdd(t, monitor, Task{ID: "l", Crit: LO, CL: 2, CH: 2, T: 4, Prio: 2})
	return monitor
}

func trace(monitor *Monitor, ticks int) string {
	result := make([]byte, ticks)
	for i := range ticks {
		id, ok := monitor.RunAt(i)
		if ok {
			result[i] = id[0]
		} else {
			result[i] = '.'
		}
	}
	return string(result)
}

func mustAdd(t *testing.T, monitor *Monitor, task Task) {
	t.Helper()
	if err := monitor.AddTask(task); err != nil {
		t.Fatalf("AddTask(%+v): %v", task, err)
	}
}

func mustSet(t *testing.T, monitor *Monitor, id string, index, demand int) {
	t.Helper()
	if err := monitor.SetDemand(id, index, demand); err != nil {
		t.Fatalf("SetDemand(%s,%d,%d): %v", id, index, demand, err)
	}
}

func mustStep(t *testing.T, monitor *Monitor, ticks int) {
	t.Helper()
	if err := monitor.Step(ticks); err != nil {
		t.Fatalf("Step(%d): %v", ticks, err)
	}
}

func TestTaskLimit(t *testing.T) {
	monitor := NewMonitor()
	for i := range 16 {
		task := Task{ID: fmt.Sprintf("t%02d", i), Crit: LO, CL: 1, CH: 1, T: 1000, Prio: i + 1}
		mustAdd(t, monitor, task)
	}
	task := Task{ID: "extra", Crit: LO, CL: 1, CH: 1, T: 1000, Prio: 17}
	if err := monitor.AddTask(task); !errors.Is(err, ErrTaskLimit) {
		t.Fatalf("AddTask extra = %v, want ErrTaskLimit", err)
	}
}
