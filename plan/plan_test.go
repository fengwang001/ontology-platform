package plan

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/name"
)

func setOf(ns ...string) map[string]struct{} {
	m := map[string]struct{}{}
	for _, n := range ns {
		m[n] = struct{}{}
	}
	return m
}

func stepsEq(got []Step, want [][2]string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i].Old != want[i][0] || got[i].New != want[i][1] {
			return false
		}
	}
	return true
}

func TestConflicts(t *testing.T) {
	cases := []struct {
		title string
		exist []string
		reqs  []Req
		want  error
	}{
		{"target exists not moved", []string{"a", "b"}, []Req{{"a", "b"}}, ErrTargetExists},
		{"two requests same new", []string{"a", "b", "c"}, []Req{{"a", "c"}, {"b", "c"}}, ErrNewDup},
		{"same old twice", []string{"a"}, []Req{{"a", "x"}, {"a", "y"}}, ErrOldDup},
		{"old missing", []string{"a"}, []Req{{"a", "b"}, {"b", "c"}}, ErrOldMissing},
		{"duplicate self loop is dup", []string{"a"}, []Req{{"a", "a"}, {"a", "a"}}, ErrOldDup},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			sp := name.New(tc.exist...)
			before := sp.Snapshot()
			_, err := Build(before, tc.reqs)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			if got := sp.Snapshot(); !reflect.DeepEqual(got, before) {
				t.Fatalf("namespace changed on conflict: %v", got)
			}
		})
	}
}

func TestOrderingDeterminismEdges(t *testing.T) {
	type wantPlan struct {
		steps  [][2]string
		cycles int
	}
	cases := []struct {
		title string
		exist []string
		reqs  []Req
		want  wantPlan
	}{
		{"empty", nil, nil, wantPlan{nil, 0}},
		{"single", []string{"a"}, []Req{{"a", "b"}}, wantPlan{[][2]string{{"a", "b"}}, 0}},
		{"chain a->b,b->c", []string{"a", "b"}, []Req{{"a", "b"}, {"b", "c"}},
			wantPlan{[][2]string{{"b", "c"}, {"a", "b"}}, 0}},
		{"self loop no-op", []string{"a"}, []Req{{"a", "a"}}, wantPlan{nil, 0}},
		{"empty string and slash names", []string{"", "x/y"},
			[]Req{{"", "x/y"}, {"x/y", "z"}},
			wantPlan{[][2]string{{"x/y", "z"}, {"", "x/y"}}, 0}},
	}
	var baseline []Step
	for i, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			p, err := Build(setOf(tc.exist...), tc.reqs)
			if err != nil {
				t.Fatal(err)
			}
			if !stepsEq(p.Steps, tc.want.steps) || len(p.Cycles) != tc.want.cycles {
				t.Fatalf("case %s: steps=%v cycles=%d", tc.title, p.Steps, len(p.Cycles))
			}
			if i == 2 {
				baseline = append([]Step(nil), p.Steps...)
			}
		})
	}
	// 确定性：打乱构造顺序 20 次，步骤序列逐元素相同（覆盖 5 个名字的链）。
	reqs := []Req{{"a", "b"}, {"b", "c"}, {"c", "d"}, {"d", "e"}, {"e", "f"}}
	exist := setOf("a", "b", "c", "d", "e")
	for iter := 0; iter < 20; iter++ {
		sh := append([]Req(nil), reqs...)
		r := rand.New(rand.NewSource(int64(iter)))
		r.Shuffle(len(sh), func(i, j int) { sh[i], sh[j] = sh[j], sh[i] })
		p, err := Build(exist, sh)
		if err != nil {
			t.Fatal(err)
		}
		if !stepsEq(p.Steps, [][2]string{{"e", "f"}, {"d", "e"}, {"c", "d"}, {"b", "c"}, {"a", "b"}}) {
			t.Fatalf("iter %d not deterministic: %v", iter, p.Steps)
		}
	if iter == 0 {
			baseline = append([]Step(nil), p.Steps...)
		} else if !reflect.DeepEqual(p.Steps, baseline) {
			t.Fatalf("iter %d differs from baseline", iter)
		}
	}
	// 无覆盖检查器：按拓扑序模拟，每步目标名必须不存在。
	sp := setOf("a", "b")
	p, _ := Build(sp, []Req{{"a", "b"}, {"b", "c"}})
	for _, st := range p.Steps {
		if _, taken := sp[st.New]; taken {
			t.Fatalf("step %v would overwrite", st)
		}
		delete(sp, st.Old)
		sp[st.New] = struct{}{}
	}
	if fmt.Sprint(len(sp)) == "" {
		t.Fatal("unreachable")
	}
}

func TestComplexity(t *testing.T) {
	const r = 50000
	reqs := make([]Req, 0, r)
	exist := map[string]struct{}{}
	for i := 0; i < r; i++ {
		o := fmt.Sprintf("n%05d", i)
		nw := fmt.Sprintf("m%05d", i)
		reqs = append(reqs, Req{o, nw})
		exist[o] = struct{}{}
	}
	p, err := Build(exist, reqs)
	if err != nil {
		t.Fatal(err)
	}
	bound := 4 * (r + len(exist))
	if p.Lookups() > bound {
		t.Fatalf("lookups %d exceed bound %d", p.Lookups(), bound)
	}
}
