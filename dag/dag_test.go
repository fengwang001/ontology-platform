package dag

import (
	"errors"
	"reflect"
	"testing"
)

func TestBuildValidationOrder(t *testing.T) {
	cases := []struct {
		name  string
		names []string
		needs [][]string
		want  error
	}{
		{"duplicate", []string{"a", "a"}, [][]string{nil, nil}, ErrDuplicateName},
		{"unknown", []string{"a", "b"}, [][]string{nil, {"zzz"}}, ErrUnknownNeed},
		{"cycle2", []string{"a", "b"}, [][]string{{"b"}, {"a"}}, ErrCycle},
		{"self-cycle", []string{"a"}, [][]string{{"a"}}, ErrCycle},
		// duplicate beats unknown dependency
		{"dup>unknown", []string{"a", "a"}, [][]string{nil, {"zzz"}}, ErrDuplicateName},
		// unknown dependency beats cycle
		{"unknown>cycle", []string{"a", "b"}, [][]string{{"b", "zzz"}, {"a"}}, ErrUnknownNeed},
		// duplicate beats cycle
		{"dup>cycle", []string{"a", "a"}, [][]string{{"a"}, {"a"}}, ErrDuplicateName},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Build(tc.names, tc.needs)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Build(%v, %v) err=%v, want class %v", tc.names, tc.needs, err, tc.want)
			}
			t.Logf("input names=%v needs=%v -> err=%v (class matched)", tc.names, tc.needs, err)
		})
	}
}

func TestBuildOKAndClosure(t *testing.T) {
	// a -> b -> d
	// a -> c -> d, e isolated
	names := []string{"a", "b", "c", "d", "e"}
	needs := [][]string{nil, {"a"}, {"a"}, {"b", "c"}, nil}
	g, err := Build(names, needs)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if g.Len() != 5 {
		t.Fatalf("Len=%d, want 5", g.Len())
	}
	idx := func(n string) int {
		i, ok := g.Index(n)
		if !ok {
			t.Fatalf("Index(%q) missing", n)
		}
		return i
	}
	if got := g.Needs(idx("d")); !reflect.DeepEqual(got, []int{idx("b"), idx("c")}) {
		t.Fatalf("Needs(d)=%v", got)
	}
	if got := g.Downstream(idx("a")); !reflect.DeepEqual(got, []int{idx("b"), idx("c")}) {
		t.Fatalf("Downstream(a)=%v", got)
	}
	wantClosure := map[string][]string{
		"a": {"b", "c", "d"},
		"b": {"d"},
		"c": {"d"},
		"d": {},
		"e": {},
	}
	for n, want := range wantClosure {
		got := []string{}
		for _, j := range g.Closure(idx(n)) {
			got = append(got, g.Name(j))
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Closure(%q)=%v, want %v", n, got, want)
		}
		t.Logf("Closure(%q)=%v", n, got)
	}
	// Duplicate needs collapse to one edge.
	g2, err := Build([]string{"a", "b"}, [][]string{nil, {"a", "a"}})
	if err != nil {
		t.Fatalf("Build dup needs: %v", err)
	}
	if got := g2.Needs(1); len(got) != 1 {
		t.Fatalf("Needs(b) with duplicate entries=%v, want 1 entry", got)
	}
}
