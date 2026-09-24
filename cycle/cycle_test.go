package cycle_test

import (
	"fmt"
	"testing"

	"ontology/cycle"
	"ontology/name"
	"ontology/plan"
)

// runNoOverwrite executes steps, failing if any target name exists
// at its step, and returns the final namespace.
func runNoOverwrite(t *testing.T, ns *name.Set, steps []plan.Step) {
	t.Helper()
	for i, s := range steps {
		if ns.Contains(s.To) {
			t.Fatalf("step %d: target %q already exists (overwrite)", i, s.To)
		}
		if err := ns.Rename(s.From, s.To); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
}

func TestBreak(t *testing.T) {
	cyclesOf := func(reqs []plan.Request) [][]plan.Request {
		steps, cycles := plan.Order(reqs)
		if len(steps) != 0 {
			t.Fatalf("expected pure cycles, got steps %v", steps)
		}
		return cycles
	}
	t.Run("two and three cycles", func(t *testing.T) {
		cases := []struct {
			name  string
			names []string
			reqs  []plan.Request
		}{
			{"two cycle", []string{"a", "b"}, []plan.Request{{From: "a", To: "b"}, {From: "b", To: "a"}}},
			{"three cycle", []string{"a", "b", "c"}, []plan.Request{{From: "a", To: "b"}, {From: "b", To: "c"}, {From: "c", To: "a"}}},
		}
		for _, tc := range cases {
			ns := name.New(tc.names...)
			before := ns.Snapshot()
			steps, temps, err := cycle.Break(ns, tc.reqs, cyclesOf(tc.reqs))
			if err != nil {
				t.Fatal(err)
			}
			if len(temps) != 1 {
				t.Fatalf("%s: temps = %v, want exactly 1", tc.name, temps)
			}
			if len(steps) != len(tc.reqs)+1 {
				t.Fatalf("%s: steps = %d, want %d", tc.name, len(steps), len(tc.reqs)+1)
			}
			runNoOverwrite(t, ns, steps)
			got, want := ns.Snapshot(), name.New(tc.names...).Snapshot()
			if fmt.Sprint(got) != fmt.Sprint(want) || fmt.Sprint(got) != fmt.Sprint(before) {
				t.Fatalf("%s: final names %v, want %v", tc.name, got, want)
			}
		}
	})
	t.Run("temp names preoccupied", func(t *testing.T) {
		names := []string{"a", "b"}
		for i := 0; i < 100; i++ {
			names = append(names, fmt.Sprintf("tmp~%d", i))
		}
		ns := name.New(names...)
		reqs := []plan.Request{{From: "a", To: "b"}, {From: "b", To: "a"}}
		steps, temps, err := cycle.Break(ns, reqs, cyclesOf(reqs))
		if err != nil {
			t.Fatal(err)
		}
		if len(temps) != 1 || temps[0] != "tmp~100" {
			t.Fatalf("temps = %v, want [tmp~100]", temps)
		}
		runNoOverwrite(t, ns, steps)
	})
	t.Run("temp count equals cycle count", func(t *testing.T) {
		var names []string
		var reqs []plan.Request
		for i := 0; i < 10; i++ {
			x, y := fmt.Sprintf("x%d", i), fmt.Sprintf("y%d", i)
			names = append(names, x, y)
			reqs = append(reqs, plan.Request{From: x, To: y}, plan.Request{From: y, To: x})
		}
		_, temps, err := cycle.Break(name.New(names...), reqs, cyclesOf(reqs))
		if err != nil {
			t.Fatal(err)
		}
		if len(temps) != 10 {
			t.Fatalf("temps = %d, want exactly 10", len(temps))
		}
	})
}
