package plan_test

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/cycle"
	"ontology/name"
	"ontology/plan"
)

// simulate 逐步执行并断言每步目标名不存在（无覆盖），返回最终负载位置。
func simulate(t *testing.T, initial []string, steps []plan.Step) map[string]string {
	t.Helper()
	at := map[string]string{}
	for _, n := range initial {
		at[n] = n
	}
	for i, s := range steps {
		if _, ok := at[s.New]; ok {
			t.Fatalf("step %d %q->%q overwrites existing name", i, s.Old, s.New)
		}
		v, ok := at[s.Old]
		if !ok {
			t.Fatalf("step %d source %q missing", i, s.Old)
		}
		delete(at, s.Old)
		at[s.New] = v
	}
	return at
}

func TestConflicts(t *testing.T) {
	cases := []struct {
		name  string
		names []string
		reqs  []plan.Request
		kind  error
		ok    func(*plan.Conflict) bool
	}{
		{"dup-old", []string{"a"}, []plan.Request{{"a", "x"}, {"a", "y"}}, plan.ErrDuplicateOld,
			func(c *plan.Conflict) bool { return c.Old == "a" && c.New == "x" && c.OtherNew == "y" }},
		{"old-missing", []string{"a"}, []plan.Request{{"z", "x"}}, plan.ErrOldMissing,
			func(c *plan.Conflict) bool { return c.Old == "z" }},
		{"dup-new", []string{"a", "b"}, []plan.Request{{"a", "x"}, {"b", "x"}}, plan.ErrDuplicateNew,
			func(c *plan.Conflict) bool { return c.New == "x" && c.Old == "a" && c.OtherOld == "b" }},
		{"target-exists", []string{"a", "b"}, []plan.Request{{"a", "b"}}, plan.ErrTargetExists,
			func(c *plan.Conflict) bool { return c.New == "b" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ns := name.New(c.names...)
			before := ns.Snapshot()
			_, err := plan.Build(ns, c.reqs)
			if !errors.Is(err, c.kind) {
				t.Fatalf("err = %v, want kind %v", err, c.kind)
			}
			var cf *plan.Conflict
			if !errors.As(err, &cf) || !c.ok(cf) {
				t.Fatalf("conflict fields wrong: %+v", cf)
			}
			if !name.Equal(ns.Snapshot(), before) {
				t.Fatal("namespace modified by conflict detection")
			}
		})
	}
}

func TestOrderAndCycles(t *testing.T) {
	cases := []struct {
		name   string
		names  []string
		reqs   []plan.Request
		steps  []plan.Step
		temps  int
		wantAt map[string]string
	}{
		{"chain", []string{"a", "b"}, []plan.Request{{"a", "b"}, {"b", "c"}},
			[]plan.Step{{Old: "b", New: "c"}, {Old: "a", New: "b"}}, 0, map[string]string{"a": "b", "b": "c"}},
		{"chain3", []string{"a", "b", "c"}, []plan.Request{{"a", "b"}, {"b", "c"}, {"c", "d"}},
			[]plan.Step{{Old: "c", New: "d"}, {Old: "b", New: "c"}, {Old: "a", New: "b"}}, 0,
			map[string]string{"a": "b", "b": "c", "c": "d"}},
		{"cycle2", []string{"a", "b"}, []plan.Request{{"a", "b"}, {"b", "a"}}, nil, 1,
			map[string]string{"a": "b", "b": "a"}},
		{"cycle3", []string{"a", "b", "c"}, []plan.Request{{"a", "b"}, {"b", "c"}, {"c", "a"}}, nil, 1,
			map[string]string{"a": "b", "b": "c", "c": "a"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := plan.Build(name.New(c.names...), c.reqs)
			if err != nil {
				t.Fatal(err)
			}
			if c.steps != nil && fmt.Sprint(p.Steps) != fmt.Sprint(c.steps) {
				t.Fatalf("steps = %v, want %v", p.Steps, c.steps)
			}
			if len(p.Temps) != c.temps {
				t.Fatalf("temps = %d, want %d", len(p.Temps), c.temps)
			}
			at := simulate(t, c.names, p.Steps)
			for payload, want := range c.wantAt {
				if at[want] != payload {
					t.Errorf("payload %q at %q, want %q", payload, at[want], want)
				}
			}
		})
	}
}

func TestTempPreoccupied(t *testing.T) {
	names := []string{"a", "b"}
	for i := range 10 {
		names = append(names, fmt.Sprintf("\x00renametmp%d", i))
	}
	p, err := plan.Build(name.New(names...), []plan.Request{{"a", "b"}, {"b", "a"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Temps) != 1 || p.Temps[0] != "\x00renametmp10" {
		t.Fatalf("temps = %v, want [\\x00renametmp10]", p.Temps)
	}
	simulate(t, names, p.Steps)
	var gen cycle.Gen
	if _, err := gen.Next(func(string) bool { return true }); !errors.Is(err, cycle.ErrNoTemp) {
		t.Fatalf("exhausted gen err = %v, want ErrNoTemp", err)
	}
}

func TestComplexity(t *testing.T) {
	const n = 50000
	names := make([]string, 0, n)
	reqs := make([]plan.Request, 0, n)
	for i := range n {
		names = append(names, fmt.Sprintf("n%d", i))
		reqs = append(reqs, plan.Request{Old: names[i], New: fmt.Sprintf("n%d", i+1)})
	}
	plan.ResetLookups()
	p, err := plan.Build(name.New(names...), reqs)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Steps) != n {
		t.Fatalf("steps = %d, want %d", len(p.Steps), n)
	}
	if got, bound := plan.Lookups(), int64(4*(n+n+1)); got > bound {
		t.Fatalf("lookups = %d, bound %d", got, bound)
	}
}

func TestTempCountEqualsCycleCount(t *testing.T) {
	var names []string
	var reqs []plan.Request
	for i := range 10 {
		a, b, c := fmt.Sprintf("c%da", i), fmt.Sprintf("c%db", i), fmt.Sprintf("c%dc", i)
		names = append(names, a, b, c)
		reqs = append(reqs, plan.Request{a, b}, plan.Request{b, c}, plan.Request{c, a})
	}
	p, err := plan.Build(name.New(names...), reqs)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Temps) != 10 {
		t.Fatalf("temps = %d, want 10", len(p.Temps))
	}
	simulate(t, names, p.Steps)
}

func TestDeterminism(t *testing.T) {
	names := []string{"a", "b", "x", "y", "p"}
	base := []plan.Request{{"a", "b"}, {"b", "c"}, {"x", "y"}, {"y", "x"}, {"p", "q"}}
	want := ""
	for seed := range 20 {
		reqs := append([]plan.Request{}, base...)
		rand.New(rand.NewSource(int64(seed))).Shuffle(len(reqs), func(i, j int) { reqs[i], reqs[j] = reqs[j], reqs[i] })
		p, err := plan.Build(name.New(names...), reqs)
		if err != nil {
			t.Fatal(err)
		}
		got := fmt.Sprint(p.Steps, p.Temps)
		if seed == 0 {
			want = got
		} else if got != want {
			t.Fatalf("seed %d: %v != %v", seed, got, want)
		}
	}
}

func TestBoundary(t *testing.T) {
	cases := []struct {
		name  string
		names []string
		reqs  []plan.Request
		steps int
		kind  error
	}{
		{"empty", []string{"a"}, nil, 0, nil},
		{"single", []string{"a"}, []plan.Request{{"a", "b"}}, 1, nil},
		{"self-loop", []string{"a"}, []plan.Request{{"a", "a"}}, 0, nil},
		{"self-loop-mixed", []string{"a"}, []plan.Request{{"a", "a"}, {"a", "b"}}, 1, nil},
		{"flood", []string{"a"}, []plan.Request{{"a", "x1"}, {"a", "x2"}, {"a", "x3"}}, 0, plan.ErrDuplicateOld},
		{"empty-name", []string{""}, []plan.Request{{"", "x"}}, 1, nil},
		{"separator", []string{"a/b"}, []plan.Request{{"a/b", "a/b/c"}}, 1, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := plan.Build(name.New(c.names...), c.reqs)
			if !errors.Is(err, c.kind) {
				t.Fatalf("err = %v, want %v", err, c.kind)
			}
			if err == nil && len(p.Steps) != c.steps {
				t.Fatalf("steps = %d, want %d", len(p.Steps), c.steps)
			}
		})
	}
}
