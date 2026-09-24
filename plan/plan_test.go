package plan

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"ontology/cycle"
	"ontology/name"
)

// simulate 逐步执行并断言任何时刻目标名都不存在（无覆盖）。
func simulate(t *testing.T, s *name.Space, steps []Step) {
	t.Helper()
	s.Lock()
	defer s.Unlock()
	for i, st := range steps {
		if s.HasLocked(st.New) {
			t.Fatalf("step %d %q->%q would overwrite", i, st.Old, st.New)
		}
		if err := s.RenameLocked(st.Old, st.New); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
}

func tempCount(steps []Step) int {
	n := 0
	for _, st := range steps {
		if cycle.IsTemp(st.Old) {
			n++
		}
	}
	return n
}

func TestConflicts(t *testing.T) {
	cases := []struct {
		desc  string
		space []string
		reqs  []Request
		want  error
		subs  []string
	}{
		{"target-exists", []string{"a", "b"}, []Request{{"a", "b"}}, ErrTargetExists, []string{`"b"`}},
		{"dup-target", []string{"a", "b"}, []Request{{"a", "c"}, {"b", "c"}}, ErrDuplicateTarget, []string{`"a"`, `"b"`}},
		{"dup-source", []string{"a"}, []Request{{"a", "b"}, {"a", "c"}}, ErrDuplicateSource, []string{`"b"`, `"c"`}},
		{"source-missing", []string{"a"}, []Request{{"zz", "b"}}, ErrSourceMissing, []string{`"zz"`}},
	}
	for _, c := range cases {
		s := name.Must(c.space...)
		before := s.Snapshot()
		_, err := Compile(s, c.reqs)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.desc, err, c.want)
		}
		for _, sub := range c.subs {
			if err == nil || !strings.Contains(err.Error(), sub) {
				t.Errorf("%s: err %v missing %s", c.desc, err, sub)
			}
		}
		got := s.Snapshot()
		if strings.Join(got, ",") != strings.Join(before, ",") {
			t.Errorf("%s: space mutated on conflict", c.desc)
		}
	}
}

func TestOrderAndCycles(t *testing.T) {
	cases := []struct {
		desc  string
		space []string
		reqs  []Request
		steps []Step
		end   []string
		temps int
	}{
		{"chain", []string{"a", "b"}, []Request{{"a", "b"}, {"b", "c"}},
			[]Step{{"b", "c"}, {"a", "b"}}, []string{"b", "c"}, 0},
		{"2-cycle", []string{"a", "b"}, []Request{{"a", "b"}, {"b", "a"}},
			nil, []string{"a", "b"}, 1},
		{"3-cycle", []string{"a", "b", "c"}, []Request{{"a", "b"}, {"b", "c"}, {"c", "a"}},
			nil, []string{"a", "b", "c"}, 1},
	}
	for _, c := range cases {
		s := name.Must(c.space...)
		p, err := Compile(s, c.reqs)
		if err != nil {
			t.Fatalf("%s: %v", c.desc, err)
		}
		if c.steps != nil && fmt.Sprint(p.Steps) != fmt.Sprint(c.steps) {
			t.Errorf("%s: steps = %v, want %v", c.desc, p.Steps, c.steps)
		}
		if n := tempCount(p.Steps); n != c.temps {
			t.Errorf("%s: temps = %d, want %d", c.desc, n, c.temps)
		}
		simulate(t, s, p.Steps)
		if strings.Join(s.Snapshot(), ",") != strings.Join(c.end, ",") {
			t.Errorf("%s: end = %v, want %v", c.desc, s.Snapshot(), c.end)
		}
	}
}

func TestEdgeCases(t *testing.T) {
	s := name.Must("a", "", "x/y")
	p, err := Compile(s, nil)
	if err != nil || len(p.Steps) != 0 {
		t.Errorf("empty: %v %v", p, err)
	}
	p, _ = Compile(s, []Request{{"a", "a"}})
	if len(p.Steps) != 0 {
		t.Errorf("self-loop should be no-op, got %v", p.Steps)
	}
	p, _ = Compile(s, []Request{{"a", "solo"}})
	if len(p.Steps) != 1 {
		t.Errorf("single request: %v", p.Steps)
	}
	p, err = Compile(s, []Request{{"", "e"}, {"x/y", "z\\w"}})
	if err != nil || len(p.Steps) != 2 {
		t.Errorf("empty/separator names: %v %v", p, err)
	}
	many := make([]Request, 100)
	for i := range many {
		many[i] = Request{"a", fmt.Sprintf("n%d", i)}
	}
	if _, err := Compile(s, many); !errors.Is(err, ErrDuplicateSource) {
		t.Errorf("many dup old: %v", err)
	}
}

func TestTempRetryAndCycleCount(t *testing.T) {
	// 预先占用全部同形临时名，仍必须成功。
	pre := []string{"a", "b"}
	for i := 0; i < 8; i++ {
		pre = append(pre, fmt.Sprintf("#rename-tmp-%d", i))
	}
	s := name.Must(pre...)
	p, err := Compile(s, []Request{{"a", "b"}, {"b", "a"}})
	if err != nil {
		t.Fatalf("temp retry: %v", err)
	}
	simulate(t, s, p.Steps)
	// 10 个互不相交的环 -> 恰好 10 个临时名。
	var names, end []string
	var reqs []Request
	for i := 0; i < 10; i++ {
		a, b := fmt.Sprintf("c%da", i), fmt.Sprintf("c%db", i)
		names = append(names, a, b)
		reqs = append(reqs, Request{a, b}, Request{b, a})
	}
	end = append(end, names...)
	s2 := name.Must(names...)
	p2, err := Compile(s2, reqs)
	if err != nil {
		t.Fatalf("10 cycles: %v", err)
	}
	if n := tempCount(p2.Steps); n != 10 {
		t.Fatalf("temps = %d, want 10", n)
	}
	simulate(t, s2, p2.Steps)
	if strings.Join(s2.Snapshot(), ",") != strings.Join(end, ",") {
		t.Error("10 cycles: final space mismatch")
	}
}

func TestDeterminismAndLookups(t *testing.T) {
	base := []Request{{"a", "b"}, {"b", "c"}, {"d", "e"}, {"f", "g"}, {"h", "i"}}
	var want []Step
	for seed := 0; seed < 20; seed++ {
		reqs := append([]Request{}, base...)
		rand.New(rand.NewSource(int64(seed))).Shuffle(len(reqs), func(i, j int) {
			reqs[i], reqs[j] = reqs[j], reqs[i]
		})
		p, err := Compile(name.Must("a", "b", "d", "f", "h"), reqs)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if want == nil {
			want = p.Steps
		} else if fmt.Sprint(p.Steps) != fmt.Sprint(want) {
			t.Fatalf("seed %d: steps differ: %v vs %v", seed, p.Steps, want)
		}
	}
	// 5 万请求，断言查找数不超过 4*(请求数+涉及名字数)。
	const n = 50000
	big := make([]Request, n)
	names := make([]string, 0, n)
	for i := 0; i < n; i++ {
		big[i] = Request{fmt.Sprintf("old%d", i), fmt.Sprintf("new%d", i)}
		names = append(names, big[i].Old)
	}
	if _, err := Compile(name.Must(names...), big); err != nil {
		t.Fatalf("big: %v", err)
	}
	if limit := 4 * (n + 2*n); lookups > limit {
		t.Fatalf("lookups = %d, limit = %d", lookups, limit)
	}
}
