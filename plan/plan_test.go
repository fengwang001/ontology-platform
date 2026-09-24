package plan

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"ontology/name"
)

// runSteps 逐步执行并断言每一步目标名都不存在（无覆盖检查器）。
func runSteps(t *testing.T, start []string, steps []name.Rename) *name.Namespace {
	t.Helper()
	ns := name.New(start...)
	for i, s := range steps {
		if ns.Has(s.New) {
			t.Fatalf("step %d: target %q already exists (overwrite)", i, s.New)
		}
		if err := ns.Transact(func(tx *name.Tx) error { return tx.Rename(s.Old, s.New) }); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	return ns
}

func TestBuildShapes(t *testing.T) {
	cases := []struct {
		name   string
		names  []string
		reqs   []name.Rename
		steps  []name.Rename // nil 表示只验证步数与结果
		nsteps int
		temps  int
		final  []string
	}{
		{"empty", nil, nil, nil, 0, 0, nil},
		{"single", []string{"a"}, []name.Rename{{Old: "a", New: "b"}}, []name.Rename{{Old: "a", New: "b"}}, 1, 0, []string{"b"}},
		{"self-loop-noop", []string{"a"}, []name.Rename{{Old: "a", New: "a"}}, nil, 0, 0, []string{"a"}},
		{"chain", []string{"a", "b"}, []name.Rename{{Old: "a", New: "b"}, {Old: "b", New: "c"}},
			[]name.Rename{{Old: "b", New: "c"}, {Old: "a", New: "b"}}, 2, 0, []string{"b", "c"}},
		{"2-cycle", []string{"a", "b"}, []name.Rename{{Old: "a", New: "b"}, {Old: "b", New: "a"}}, nil, 3, 1, []string{"a", "b"}},
		{"3-cycle", []string{"a", "b", "c"}, []name.Rename{{Old: "a", New: "b"}, {Old: "b", New: "c"}, {Old: "c", New: "a"}}, nil, 4, 1, []string{"a", "b", "c"}},
		{"empty-name", []string{""}, []name.Rename{{Old: "", New: "x"}}, []name.Rename{{Old: "", New: "x"}}, 1, 0, []string{"x"}},
		{"separator", []string{"a/b"}, []name.Rename{{Old: "a/b", New: `c\d`}}, []name.Rename{{Old: "a/b", New: `c\d`}}, 1, 0, []string{`c\d`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := Build(name.New(c.names...), c.reqs)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if p.Temps != c.temps {
				t.Errorf("temps = %d, want %d", p.Temps, c.temps)
			}
			if len(p.Steps) != c.nsteps {
				t.Errorf("nsteps = %d, want %d", len(p.Steps), c.nsteps)
			}
			if c.steps != nil && !slices.Equal(p.Steps, c.steps) {
				t.Errorf("steps = %v, want %v", p.Steps, c.steps)
			}
			if got := runSteps(t, c.names, p.Steps); !got.Equal(name.New(c.final...)) {
				t.Errorf("final = %v, want %v", got.Snapshot(), c.final)
			}
		})
	}
}

func TestConflicts(t *testing.T) {
	many := make([]name.Rename, 0, 100000)
	for i := 0; i < 100000; i++ {
		many = append(many, name.Rename{Old: fmt.Sprintf("n%d", i%3), New: fmt.Sprintf("m%d", i)})
	}
	cases := []struct {
		name  string
		names []string
		reqs  []name.Rename
		sent  error
		msg   []string
	}{
		{"target-exists", []string{"a", "z"}, []name.Rename{{Old: "a", New: "z"}}, ErrTargetExists, []string{`"z"`}},
		{"dup-target", []string{"a", "b"}, []name.Rename{{Old: "a", New: "x"}, {Old: "b", New: "x"}}, ErrDuplicateTarget, []string{`"a"`, `"b"`}},
		{"dup-source", []string{"a"}, []name.Rename{{Old: "a", New: "x"}, {Old: "a", New: "y"}}, ErrDuplicateSource, []string{`"x"`, `"y"`}},
		{"source-missing", []string{"a"}, []name.Rename{{Old: "ghost", New: "x"}}, ErrSourceMissing, []string{`"ghost"`}},
		{"self-loop-missing", []string{"a"}, []name.Rename{{Old: "ghost", New: "ghost"}}, ErrSourceMissing, []string{`"ghost"`}},
		{"many-dup-source", []string{"n0", "n1", "n2"}, many, ErrDuplicateSource, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ns := name.New(c.names...)
			before := ns.Snapshot()
			_, err := Build(ns, c.reqs)
			if !errors.Is(err, c.sent) {
				t.Fatalf("err = %v, want %v", err, c.sent)
			}
			for _, m := range c.msg {
				if !strings.Contains(err.Error(), m) {
					t.Errorf("err %q missing %s", err, m)
				}
			}
			if !slices.Equal(before, ns.Snapshot()) {
				t.Error("namespace modified by rejected build")
			}
		})
	}
}

func TestLookupsLinear(t *testing.T) {
	const n = 50000
	names := make([]string, 0, n)
	reqs := make([]name.Rename, 0, n)
	for i := 0; i < n; i++ {
		a := fmt.Sprintf("a%d", i)
		names = append(names, a)
		reqs = append(reqs, name.Rename{Old: a, New: fmt.Sprintf("b%d", i)})
	}
	ResetLookups()
	if _, err := Build(name.New(names...), reqs); err != nil {
		t.Fatal(err)
	}
	if got, limit := Lookups(), int64(4*(n+2*n)); got > limit {
		t.Errorf("lookups = %d, limit %d", got, limit)
	}
}

func TestTempCountAndPreoccupied(t *testing.T) {
	cases := []struct {
		name        string
		cycles      int
		preoccupied int
	}{
		{"ten-3-cycles", 10, 0},
		{"one-2-cycle-all-temps-taken", 1, 32},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var names []string
			var reqs []name.Rename
			for i := 0; i < c.preoccupied; i++ {
				names = append(names, fmt.Sprintf(".tmp-rename-%d", i))
			}
			for i := 0; i < c.cycles; i++ {
				x, y, z := fmt.Sprintf("x%d", i), fmt.Sprintf("y%d", i), fmt.Sprintf("z%d", i)
				names = append(names, x, y, z)
				reqs = append(reqs, name.Rename{Old: x, New: y}, name.Rename{Old: y, New: z}, name.Rename{Old: z, New: x})
			}
			p, err := Build(name.New(names...), reqs)
			if err != nil {
				t.Fatal(err)
			}
			if p.Temps != c.cycles {
				t.Errorf("temps = %d, want %d (one per cycle)", p.Temps, c.cycles)
			}
			runSteps(t, names, p.Steps)
		})
	}
}

func TestDeterministic(t *testing.T) {
	base := []name.Rename{{Old: "a", New: "b"}, {Old: "b", New: "c"}, {Old: "c", New: "a"},
		{Old: "d", New: "e"}, {Old: "e", New: "f"}, {Old: "g", New: "h"}}
	var want []name.Rename
	for i := 0; i < 20; i++ {
		sh := slices.Clone(base)
		rand.Shuffle(len(sh), func(i, j int) { sh[i], sh[j] = sh[j], sh[i] })
		p, err := Build(name.New("a", "b", "c", "d", "e", "g"), sh)
		if err != nil {
			t.Fatal(err)
		}
		if want == nil {
			want = p.Steps
		} else if !slices.Equal(want, p.Steps) {
			t.Fatalf("run %d: steps = %v, want %v", i, p.Steps, want)
		}
	}
}
