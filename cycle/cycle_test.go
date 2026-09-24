package cycle

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"ontology/name"
	"ontology/plan"
)

func analyzeForCycle(t *testing.T, initial []string, reqs []plan.Request) *plan.Plan {
	t.Helper()
	p, err := plan.Analyze(name.New(initial...), reqs)
	if !errors.Is(err, plan.ErrCycle) {
		t.Fatalf("err = %v, want cycle", err)
	}
	return p
}

func executeCheck(steps []plan.Step, initial []string) bool {
	current := map[string]struct{}{}
	for _, item := range initial {
		current[item] = struct{}{}
	}
	for _, step := range steps {
		if _, busy := current[step.To]; busy {
			return false
		}
		delete(current, step.From)
		current[step.To] = struct{}{}
	}
	return true
}

func TestBreakCyclesTable(t *testing.T) {
	tests := []struct {
		name     string
		initial  []string
		requests []plan.Request
		temp     int
		final    []string
	}{
		{"binary", []string{"a", "b"}, []plan.Request{{"a", "b"}, {"b", "a"}}, 1, []string{"a", "b"}},
		{"ternary", []string{"a", "b", "c"}, []plan.Request{{"a", "b"}, {"b", "c"}, {"c", "a"}}, 1, []string{"a", "b", "c"}},
		{"chain-and-cycle", []string{"a", "b", "c", "x"}, []plan.Request{{"a", "b"}, {"b", "c"}, {"c", "a"}, {"x", "d"}}, 1, []string{"a", "b", "c", "d"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := analyzeForCycle(t, tt.initial, tt.requests)
			got, err := Break(p, 1024)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.TempNames) != tt.temp || !executeCheck(got.Steps, tt.initial) {
				t.Fatalf("temps=%d safe=%v steps=%#v", len(got.TempNames), executeCheck(got.Steps, tt.initial), got.Steps)
			}
		})
	}
}

func TestTempRetryAndCycleCount(t *testing.T) {
	p := analyzeForCycle(t, []string{"a", "b"}, []plan.Request{{"a", "b"}, {"b", "a"}})
	if _, err := Break(p, 0); !errors.Is(err, ErrNoTemp) {
		t.Fatalf("err = %v, want ErrNoTemp", err)
	}

	initial := []string{"a", "b"}
	for i := 0; i < 1024; i++ {
		initial = append(initial, fmt.Sprintf("__rename_tmp_%d__", i))
	}
	blocked := analyzeForCycle(t, initial, []plan.Request{{"a", "b"}, {"b", "a"}})
	if _, err := Break(blocked, 1024); !errors.Is(err, ErrNoTemp) {
		t.Fatalf("blocked err = %v", err)
	}

	var names []string
	var reqs []plan.Request
	for i := 0; i < 10; i++ {
		left, right := fmt.Sprintf("l%d", i), fmt.Sprintf("r%d", i)
		names = append(names, left, right)
		reqs = append(reqs, plan.Request{left, right}, plan.Request{right, left})
	}
	got, err := Break(analyzeForCycle(t, names, reqs), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.TempNames) != 10 {
		t.Fatalf("temps = %d, want 10", len(got.TempNames))
	}
	if !executeCheck(got.Steps, names) {
		t.Fatalf("unsafe steps: %#v", got.Steps)
	}
}

func TestBreakDeterministic(t *testing.T) {
	reqs := []plan.Request{{"a", "b"}, {"b", "c"}, {"c", "a"}, {"x", "y"}, {"y", "x"}}
	first, err := Break(analyzeForCycle(t, []string{"a", "b", "c", "x", "y"}, reqs), 1024)
	if err != nil {
		t.Fatal(err)
	}
	for seed := int64(0); seed < 20; seed++ {
		shuffled := append([]plan.Request(nil), reqs...)
		for i := len(shuffled) - 1; i > 0; i-- {
			j := int(seed % int64(i+1))
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		}
		got, err := Break(analyzeForCycle(t, []string{"a", "b", "c", "x", "y"}, shuffled), 1024)
		if err != nil || !reflect.DeepEqual(got.Steps, first.Steps) {
			t.Fatalf("seed %d: %#v err=%v", seed, got, err)
		}
	}
}
