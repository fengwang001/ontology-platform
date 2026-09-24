package cycle

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"ontology/plan"
)

func setOf(ns ...string) map[string]struct{} {
	m := map[string]struct{}{}
	for _, n := range ns {
		m[n] = struct{}{}
	}
	return m
}

func simulate(initial map[string]struct{}, steps []plan.Step) (map[string]struct{}, error) {
	sim := map[string]struct{}{}
	for n := range initial {
		sim[n] = struct{}{}
	}
	for _, st := range steps {
		if _, taken := sim[st.New]; taken {
			return nil, fmt.Errorf("overwrite at %v->%v", st.Old, st.New)
		}
		if _, ok := sim[st.Old]; !ok {
			return nil, fmt.Errorf("missing old %v", st.Old)
		}
		delete(sim, st.Old)
		sim[st.New] = struct{}{}
	}
	return sim, nil
}

func TestExpand(t *testing.T) {
	cases := []struct {
		title  string
		exist  []string
		reqs   []plan.Req
		temps  int
		result map[string]struct{}
	}{
		{"two-cycle", []string{"a", "b"}, []plan.Req{{"a", "b"}, {"b", "a"}},
			1, setOf("a", "b")},
		{"three-cycle", []string{"a", "b", "c"},
			[]plan.Req{{"a", "b"}, {"b", "c"}, {"c", "a"}},
			1, map[string]struct{}{"b": {}, "c": {}, "a": {}}},
		{"ten-disjoint-cycles", nil, nil, 10, nil},
		{"all-temp-names-taken", []string{"a", "b"},
			[]plan.Req{{"a", "b"}, {"b", "a"}}, 1, setOf("a", "b")},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			var exist map[string]struct{}
			var reqs []plan.Req
			switch tc.title {
			case "ten-disjoint-cycles":
				exist = map[string]struct{}{}
				for i := 0; i < 10; i++ {
					x := fmt.Sprintf("x%02d", i)
					y := fmt.Sprintf("y%02d", i)
					exist[x], exist[y] = struct{}{}, struct{}{}
					reqs = append(reqs, plan.Req{x, y}, plan.Req{y, x})
				}
			case "all-temp-names-taken":
				exist = setOf(tc.exist...)
				// 人为占用从 tpm-0 开始的全部候选，前 1000 个。
				for i := 0; i < 1000; i++ {
					exist[tempPrefix+itoa(i)] = struct{}{}
				}
				reqs = tc.reqs
			default:
				exist, reqs = setOf(tc.exist...), tc.reqs
			}
			p, err := plan.Build(exist, reqs)
			if err != nil {
				t.Fatal(err)
			}
			e, err := Expand(p, exist, reqs)
			if err != nil {
				t.Fatal(err)
			}
			if e.TempCount() != tc.temps {
				t.Fatalf("temps=%d want %d", e.TempCount(), tc.temps)
			}
			steps := LinearSteps(p, e)
			got, err := simulate(exist, steps)
			if err != nil {
				t.Fatalf("%s: %v; steps=%v", tc.title, err, steps)
			}
			want := tc.result
			if tc.title == "ten-disjoint-cycles" {
				want = exist
			}
			if tc.title == "all-temp-names-taken" {
				want = map[string]struct{}{"a": {}, "b": {}}
				for i := 0; i < 1000; i++ {
					want[tempPrefix+itoa(i)] = struct{}{}
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("final set %v want %v", got, want)
			}
			if tc.title == "all-temp-names-taken" && e.Temps[0] != tempPrefix+"1000" {
				t.Fatalf("retry did not skip occupied names: %s", e.Temps[0])
			}
		})
	}
}

func TestTempErrorSentinel(t *testing.T) {
	if !errors.Is(ErrNoTempName, ErrNoTempName) {
		t.Fatal("sentinel mismatch")
	}
}
