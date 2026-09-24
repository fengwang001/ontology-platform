package plan

import (
	"errors"
	"fmt"
	"testing"
)

func steps(ss []string) []Step {
	out := make([]Step, 0, len(ss)/2)
	for i := 0; i < len(ss); i += 2 {
		out = append(out, Step{Old: ss[i], New: ss[i+1]})
	}
	return out
}

func TestAnalyzeOrdering(t *testing.T) {
	cases := []struct {
		name string
		have []string
		reqs []Req
		want []Step
		cyc  int
	}{
		{"empty", nil, nil, nil, 0},
		{"single", []string{"a"}, []Req{{"a", "b"}}, steps([]string{"a", "b"}), 0},
		{"chain", []string{"a", "b"}, []Req{{"a", "b"}, {"b", "c"}},
			steps([]string{"b", "c", "a", "b"}), 0},
		{"two chains lex", []string{"a", "b", "x", "y"},
			[]Req{{"a", "b"}, {"b", "c"}, {"x", "y"}, {"y", "z"}},
			steps([]string{"b", "c", "a", "b", "y", "z", "x", "y"}), 0},
		{"two-cycle", []string{"a", "b"}, []Req{{"a", "b"}, {"b", "a"}}, nil, 1},
		{"three-cycle", []string{"a", "b", "c"},
			[]Req{{"a", "b"}, {"b", "c"}, {"c", "a"}}, nil, 1},
		{"self loop noop", []string{"a"}, []Req{{"a", "a"}}, nil, 0},
		{"empty string name", []string{""}, []Req{{"", "x"}}, steps([]string{"", "x"}), 0},
		{"slash ordinary", []string{"a/b"}, []Req{{"a/b", "c/d"}},
			steps([]string{"a/b", "c/d"}), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Analyze(tc.have, tc.reqs)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(p.Steps) != len(tc.want) {
				t.Fatalf("steps=%v want %v", p.Steps, tc.want)
			}
			for i := range tc.want {
				if p.Steps[i] != tc.want[i] {
					t.Fatalf("step %d = %v want %v (all=%v)", i, p.Steps[i], tc.want[i], p.Steps)
				}
			}
			if len(p.Cycles) != tc.cyc {
				t.Fatalf("cycles=%v want %d", p.Cycles, tc.cyc)
			}
		})
	}
}

func TestConflicts(t *testing.T) {
	cases := []struct {
		name string
		have []string
		reqs []Req
		kind error
	}{
		{"target exists", []string{"a", "b"}, []Req{{"a", "b"}}, ErrTargetExists},
		{"dup target", []string{"a", "b"}, []Req{{"a", "x"}, {"b", "x"}}, ErrDupTarget},
		{"dup source", []string{"a"}, []Req{{"a", "x"}, {"a", "y"}}, ErrDupSource},
		{"missing source", []string{"a"}, []Req{{"b", "x"}}, ErrMissingSource},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := append([]string(nil), tc.have...)
			_, err := Analyze(tc.have, tc.reqs)
			if err == nil || !errors.Is(err, tc.kind) {
				t.Fatalf("err=%v want kind %v", err, tc.kind)
			}
			if len(before) != len(tc.have) {
				t.Fatalf("snapshot mutated: %v -> %v", before, tc.have)
			}
		})
	}
}

func TestLinearLookups(t *testing.T) {
	const n = 50000
	have := make([]string, n)
	reqs := make([]Req, n)
	for i := 0; i < n; i++ {
		have[i] = fmt.Sprintf("n%d", i)
		reqs[i] = Req{Old: fmt.Sprintf("n%d", i), New: fmt.Sprintf("m%d", i)}
	}
	p, err := Analyze(have, reqs)
	if err != nil {
		t.Fatal(err)
	}
	names := 2 * n
	bound := 4 * (n + names)
	if p.Lookups() > bound {
		t.Fatalf("lookups=%d bound=%d", p.Lookups(), bound)
	}
}
