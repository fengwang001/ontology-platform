package mcsched

import (
	"strings"
	"testing"
)

// traceOf renders the first n ticks of the monitor's run, '.' for an
// idle tick.
func traceOf(m *Monitor, n int) string {
	var b strings.Builder
	for t := 0; t < n; t++ {
		id := m.RunAt(t)
		if id == "" {
			b.WriteByte('.')
		} else {
			b.WriteString(id)
		}
	}
	return b.String()
}

func mustAdd(t *testing.T, m *Monitor, spec TaskSpec) {
	t.Helper()
	if err := m.AddTask(spec); err != nil {
		t.Fatalf("AddTask(%+v) = %v, want nil", spec, err)
	}
}

func mustDemand(t *testing.T, m *Monitor, id string, k, c int) {
	t.Helper()
	if err := m.SetDemand(id, k, c); err != nil {
		t.Fatalf("SetDemand(%q, %d, %d) = %v, want nil", id, k, c, err)
	}
}

func mustStep(t *testing.T, m *Monitor, n int) {
	t.Helper()
	if err := m.Step(n); err != nil {
		t.Fatalf("Step(%d) = %v, want nil", n, err)
	}
}

func checkStats(t *testing.T, m *Monitor, want Stats) {
	t.Helper()
	if got := m.Stats(); got != want {
		t.Fatalf("Stats() = %+v, want %+v", got, want)
	}
}

// Spec example 1: demand 5 > CL=2, so the system switches to HI at the
// end of tick 1 (time 2), discards the running LO job, skips the LO
// release at t=4, and recovers to LO at t=5 when the HI job completes.
func TestSpecExampleSwitch(t *testing.T) {
	m := NewMonitor()
	mustAdd(t, m, TaskSpec{ID: "h", Crit: HI, CL: 2, CH: 5, T: 10, Prio: 1, Phi: 0})
	mustAdd(t, m, TaskSpec{ID: "l", Crit: LO, CL: 2, CH: 2, T: 4, Prio: 2, Phi: 0})
	mustDemand(t, m, "h", 0, 5)
	// Step 12 ticks: h runs 5 ticks, idle 3, l runs 2, h runs 2.
	mustStep(t, m, 12)
	if got := traceOf(m, 12); got != "hhhhh...llhh" {
		t.Fatalf("trace = %q, want %q", got, "hhhhh...llhh")
	}
}

// Spec example 2: demand exactly CL=2 completes without a mode switch.
func TestSpecExampleExactCLNoSwitch(t *testing.T) {
	m := NewMonitor()
	mustAdd(t, m, TaskSpec{ID: "h", Crit: HI, CL: 2, CH: 5, T: 10, Prio: 1, Phi: 0})
	mustAdd(t, m, TaskSpec{ID: "l", Crit: LO, CL: 2, CH: 2, T: 4, Prio: 2, Phi: 0})
	mustDemand(t, m, "h", 0, 2)
	// h: ticks 0-1; l: 2-3; l again 4-5; idle 6-7; l 8-9; h 10-11.
	mustStep(t, m, 12)
	if got := traceOf(m, 12); got != "hhllll..llhh" {
		t.Fatalf("trace = %q, want %q", got, "hhllll..llhh")
	}
	checkStats(t, m, Stats{Completed: 5})
}

// Spec example 3: the HI job completes at t=4; the same tick first
// recovers to LO and only then releases the LO job, so it is not
// skipped.
func TestSpecExampleRestoreBeforeRelease(t *testing.T) {
	m := NewMonitor()
	mustAdd(t, m, TaskSpec{ID: "h", Crit: HI, CL: 2, CH: 4, T: 10, Prio: 1, Phi: 0})
	mustAdd(t, m, TaskSpec{ID: "l", Crit: LO, CL: 2, CH: 2, T: 4, Prio: 2, Phi: 0})
	mustDemand(t, m, "h", 0, 4)
	// h: ticks 0-3; l: 4-5; idle 6-7; l 8-9; h 10-11.
	mustStep(t, m, 12)
	if got := traceOf(m, 12); got != "hhhhll..llhh" {
		t.Fatalf("trace = %q, want %q", got, "hhhhll..llhh")
	}
}
