package sch

import "testing"

// history builds m versions: "base" in every version and a field "late"
// introduced only in the final version, so resolving def(late, 1) is the
// missing-field case (field born far in the future).
func history(m int) []map[string]int {
	vs := make([]map[string]int, m)
	for v := range vs {
		vs[v] = map[string]int{"base": 1}
	}
	vs[m-1]["late"] = 7
	return vs
}

// TestProbeConstantBound verifies that the number of version entries
// inspected while resolving one missing default does not grow with history
// length m: it must stay at a small constant across m = 100..10000.
func TestProbeConstantBound(t *testing.T) {
	first := int64(-1)
	for _, m := range []int{100, 1000, 10000} {
		tab := New(history(m))
		if d, ok := tab.Def("late", 1); !ok || d != 7 {
			t.Fatalf("m=%d: def(late,1)=%d,%v, want 7,true", m, d, ok)
		}
		got := tab.probe.Load() // unexported counter, read only from inside sch
		if got > 2 {
			t.Fatalf("m=%d: inspected %d entries, want constant <= 2", m, got)
		}
		if first == -1 {
			first = got
		} else if got != first {
			t.Fatalf("m=%d: inspected %d, want same constant %d as m=100", m, got, first)
		}
	}
}

// TestDefSemantics checks frozen defaults across introduction, redefinition
// and removal on the v1..v3 schema table.
func TestDefSemantics(t *testing.T) {
	tab := New([]map[string]int{
		{"a": 1, "b": 2},
		{"a": 1, "b": 2, "c": 10},
		{"a": 1, "c": 30, "d": 20},
	})
	cases := []struct {
		f    string
		w    int
		want int
	}{
		{"c", 1, 10}, // unborn at W=1: introduction default, frozen
		{"c", 2, 10},
		{"c", 3, 30}, // redefined
		{"b", 3, 2},  // removed at W=3: last pre-removal default
		{"d", 1, 20}, // unborn: introduction default
		{"d", 2, 20},
		{"a", 3, 1},
	}
	for _, c := range cases {
		if got, ok := tab.Def(c.f, c.w); !ok || got != c.want {
			t.Errorf("Def(%q,%d)=%d,%v, want %d", c.f, c.w, got, ok, c.want)
		}
	}
	if _, ok := tab.Def("nope", 1); ok {
		t.Error("Def for never-existing field must report !ok")
	}
}
