package plan

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"ontology/name"
)

func TestAnalyzeTable(t *testing.T) {
	tests := []struct {
		name     string
		initial  []string
		requests []Request
		want     []Step
		err      error
	}{
		{"empty", nil, nil, nil, nil},
		{"single", []string{"a"}, []Request{{"a", "b"}}, []Step{{"a", "b"}}, nil},
		{"self noop", []string{"a"}, []Request{{"a", "a"}}, nil, nil},
		{"empty name", []string{""}, []Request{{"", "b"}}, []Step{{"", "b"}}, nil},
		{"path chars", []string{"a/b"}, []Request{{"a/b", `c\d`}}, []Step{{"a/b", `c\d`}}, nil},
		{"chain", []string{"a", "b"}, []Request{{"a", "b"}, {"b", "c"}}, []Step{{"b", "c"}, {"" + "a", "b"}}, nil},
		{"missing", []string{"b"}, []Request{{"a", "c"}}, nil, ErrMissingSource},
		{"occupied", []string{"a", "b"}, []Request{{"a", "b"}}, nil, ErrTargetExists},
		{"duplicate source", []string{"a"}, []Request{{"a", "b"}, {"a", "c"}}, nil, ErrDuplicateSource},
		{"duplicate target", []string{"a", "b"}, []Request{{"a", "x"}, {"b", "x"}}, nil, ErrDuplicateTarget},
		{"cycle", []string{"a", "b"}, []Request{{"a", "b"}, {"b", "a"}}, nil, ErrCycle},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := append([]string(nil), tt.initial...)
			got, err := Analyze(name.New(tt.initial...), tt.requests)
			if !errors.Is(err, tt.err) {
				t.Fatalf("err = %v, want %v", err, tt.err)
			}
			if err == nil && !reflect.DeepEqual(got.Steps, tt.want) {
				t.Fatalf("steps = %#v, want %#v", got.Steps, tt.want)
			}
			if !reflect.DeepEqual(before, tt.initial) {
				t.Fatalf("input mutated: %#v", before)
			}
		})
	}
}

func TestConflictDoesNotMutateAndIsDeterministic(t *testing.T) {
	cases := []struct {
		name string
		want error
		reqs []Request
	}{
		{"missing", ErrMissingSource, []Request{{"missing", "x"}}},
		{"occupied", ErrTargetExists, []Request{{"a", "b"}}},
		{"dup-source", ErrDuplicateSource, []Request{{"a", "x"}, {"" + "a", "y"}}},
		{"dup-target", ErrDuplicateTarget, []Request{{"a", "z"}, {"b", "z"}}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			ns := name.New("a", "b")
			_, err := Analyze(ns, tt.reqs)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v", err)
			}
			if !name.Equal(ns, name.New("a", "b")) {
				t.Fatalf("namespace changed: %#v", ns.Snapshot())
			}
		})
	}

	base := []Request{{"a", "b"}, {"b", "c"}, {"d", "e"}, {"c", "f"}}
	first, err := Analyze(name.New("a", "b", "c", "d"), base)
	if err != nil {
		t.Fatal(err)
	}
	for seed := int64(0); seed < 20; seed++ {
		reqs := append([]Request(nil), base...)
		for i := len(reqs) - 1; i > 0; i-- {
			j := int(seed % int64(i+1))
			reqs[i], reqs[j] = reqs[j], reqs[i]
		}
		got, err := Analyze(name.New("a", "b", "c", "d"), reqs)
		if err != nil || !reflect.DeepEqual(got.Steps, first.Steps) {
			t.Fatalf("seed %d: %#v err=%v", seed, got.Steps, err)
		}
	}
}

func TestLookupComplexity(t *testing.T) {
	const n = 50000
	var initial []string
	var reqs []Request
	for i := 0; i < n; i++ {
		from := fmt.Sprintf("a%05d", i)
		to := fmt.Sprintf("b%05d", i)
		initial = append(initial, from)
		reqs = append(reqs, Request{from, to})
	}
	p, err := Analyze(name.New(initial...), reqs)
	if err != nil {
		t.Fatal(err)
	}
	limit := int64(4 * (len(reqs) + len(p.Names)))
	if p.LookupCount() > limit {
		t.Fatalf("lookups %d > %d", p.LookupCount(), limit)
	}
}
