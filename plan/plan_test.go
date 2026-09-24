package plan

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/name"
)

func TestCheck(t *testing.T) {
	cases := []struct {
		name string
		ns   []string
		reqs []Request
		want error // nil means accepted
	}{
		{"empty batch", []string{"a"}, nil, nil},
		{"single", []string{"a"}, []Request{{"a", "b"}}, nil},
		{"self loop is no-op", []string{"a"}, []Request{{"a", "a"}}, nil},
		{"self loop dropped then target exists", []string{"a", "b"}, []Request{{"a", "a"}, {"b", "a"}}, ErrTargetExists},
		{"empty name legal", []string{""}, []Request{{"", "x"}}, nil},
		{"separator is ordinary", []string{"a/b"}, []Request{{"a/b", "c/d"}}, nil},
		{"chain ok", []string{"a", "b"}, []Request{{"a", "b"}, {"b", "c"}}, nil},
		{"target exists not moved", []string{"x", "a"}, []Request{{"x", "a"}}, ErrTargetExists},
		{"target moved away ok", []string{"x", "a"}, []Request{{"x", "a"}, {"a", "b"}}, nil},
		{"dup target", []string{"x", "y"}, []Request{{"x", "a"}, {"y", "a"}}, ErrDuplicateTarget},
		{"dup source", []string{"x"}, []Request{{"x", "a"}, {"x", "b"}}, ErrDuplicateSource},
		{"missing source", []string{"a"}, []Request{{"ghost", "b"}}, ErrMissingSource},
		{"many reqs few names hits dup source", []string{"a"}, []Request{{"a", "b"}, {"a", "c"}, {"a", "d"}}, ErrDuplicateSource},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns := name.New(tc.ns...)
			before := ns.Snapshot()
			err := Check(ns, tc.reqs)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Check err = %v, want %v", err, tc.want)
			}
			if !reflect.DeepEqual(ns.Snapshot(), before) {
				t.Fatalf("namespace changed during Check: %v -> %v", before, ns.Snapshot())
			}
			if tc.want == nil {
				return
			}
			var ce *ConflictError
			if !errors.As(err, &ce) {
				t.Fatalf("err %v is not a ConflictError", err)
			}
			switch tc.want {
			case ErrDuplicateSource:
				if ce.To == "" || ce.To2 == "" || ce.To == ce.To2 {
					t.Fatalf("dup source must report both new names, got %+v", ce)
				}
			case ErrDuplicateTarget:
				if ce.From == "" || ce.From2 == "" || ce.From == ce.From2 {
					t.Fatalf("dup target must report both old names, got %+v", ce)
				}
			case ErrMissingSource:
				if ce.From != "ghost" {
					t.Fatalf("missing source must name the ghost, got %+v", ce)
				}
			}
		})
	}
}

func TestOrder(t *testing.T) {
	cases := []struct {
		name      string
		reqs      []Request
		wantSteps []Step
		wantCyc   int // number of cycles
		cycLen    int // length of first cycle, 0 if none
	}{
		{"empty", nil, nil, 0, 0},
		{"single", []Request{{"a", "b"}}, []Step{{"a", "b"}}, 0, 0},
		{"self loop dropped", []Request{{"a", "a"}}, nil, 0, 0},
		{"chain", []Request{{"a", "b"}, {"b", "c"}}, []Step{{"b", "c"}, {"a", "b"}}, 0, 0},
		{"independent sorted", []Request{{"b", "y"}, {"a", "x"}}, []Step{{"a", "x"}, {"b", "y"}}, 0, 0},
		{"two cycle", []Request{{"a", "b"}, {"b", "a"}}, nil, 1, 2},
		{"three cycle", []Request{{"a", "b"}, {"b", "c"}, {"c", "a"}}, nil, 1, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			steps, cycles := Order(tc.reqs)
			if !reflect.DeepEqual(steps, tc.wantSteps) {
				t.Fatalf("steps = %v, want %v", steps, tc.wantSteps)
			}
			if len(cycles) != tc.wantCyc {
				t.Fatalf("cycles = %d, want %d", len(cycles), tc.wantCyc)
			}
			if tc.cycLen > 0 && len(cycles[0]) != tc.cycLen {
				t.Fatalf("cycle length = %d, want %d", len(cycles[0]), tc.cycLen)
			}
		})
	}
}

func TestDeterministic(t *testing.T) {
	base := []Request{{"a", "b"}, {"b", "c"}, {"d", "e"}, {"p", "q"}, {"q", "r"}, {"r", "p"}}
	wantSteps, wantCycles := Order(base)
	for i := 0; i < 20; i++ {
		sh := append([]Request(nil), base...)
		rand.New(rand.NewSource(int64(i))).Shuffle(len(sh), func(a, b int) { sh[a], sh[b] = sh[b], sh[a] })
		steps, cycles := Order(sh)
		if !reflect.DeepEqual(steps, wantSteps) || !reflect.DeepEqual(cycles, wantCycles) {
			t.Fatalf("shuffle %d changed order: %v %v", i, steps, cycles)
		}
	}
}

func TestComplexity(t *testing.T) {
	const n = 50000
	names := make([]string, 0, n)
	reqs := make([]Request, 0, n)
	for i := 0; i < n; i++ {
		names = append(names, fmt.Sprintf("n%d", i))
		reqs = append(reqs, Request{From: fmt.Sprintf("n%d", i), To: fmt.Sprintf("n%d", i+1)})
	}
	resetLookups()
	if err := Check(name.New(names...), reqs); err != nil {
		t.Fatal(err)
	}
	Order(reqs)
	distinct := n + 1
	if got, limit := lookups, int64(4*(n+distinct)); got > limit {
		t.Fatalf("lookups = %d, limit = %d", got, limit)
	}
}
