package api

import (
	"errors"
	"maps"
	"reflect"
	"sync"
	"testing"
)

func TestNewErrors(t *testing.T) {
	cases := []struct {
		name string
		defs []Def
		want error
	}{
		{"empty name", []Def{{Kind: "src"}}, ErrInvalidDef},
		{"dup name", []Def{{Name: "a", Kind: "src"}, {Name: "a", Kind: "src"}}, ErrInvalidDef},
		{"dup input", []Def{{Name: "a", Kind: "src"}, {Name: "b", Kind: "sum", Inputs: []string{"a", "a"}}}, ErrInvalidDef},
		{"src with input", []Def{{Name: "a", Kind: "src", Inputs: []string{"a"}}}, ErrInvalidDef},
		{"scale arity", []Def{{Name: "a", Kind: "src"}, {Name: "b", Kind: "scale", Inputs: []string{"a", "a"}, K: 2}}, ErrInvalidDef},
		{"sum arity", []Def{{Name: "b", Kind: "sum"}}, ErrInvalidDef},
		{"bad kind", []Def{{Name: "a", Kind: "mul"}}, ErrInvalidDef},
		{"unknown input", []Def{{Name: "a", Kind: "src"}, {Name: "b", Kind: "add", Inputs: []string{"z"}, K: 1}}, ErrUnknownNode},
		{"cycle", []Def{{Name: "a", Kind: "src"}, {Name: "b", Kind: "add", Inputs: []string{"c"}, K: 1}, {Name: "c", Kind: "add", Inputs: []string{"b"}, K: 1}}, ErrCycle},
		{"self loop", []Def{{Name: "a", Kind: "add", Inputs: []string{"a"}, K: 1}}, ErrCycle},
		{"invalid beats cycle", []Def{{Name: "", Kind: "src"}, {Name: "b", Kind: "add", Inputs: []string{"b"}, K: 1}}, ErrInvalidDef},
		{"unknown beats cycle", []Def{{Name: "a", Kind: "add", Inputs: []string{"b"}, K: 1}, {Name: "c", Kind: "add", Inputs: []string{"c"}, K: 1}}, ErrUnknownNode},
	}
	for _, tc := range cases {
		if _, err := New(tc.defs); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
	sentinels := []error{ErrInvalidDef, ErrUnknownNode, ErrCycle, ErrNotSource}
	for i, e1 := range sentinels {
		for j, e2 := range sentinels {
			if i != j && errors.Is(e1, e2) {
				t.Errorf("sentinels %v and %v must be distinct", e1, e2)
			}
		}
	}
}

func TestApplyErrors(t *testing.T) {
	a, err := New(seven)
	if err != nil {
		t.Fatal(err)
	}
	before := a.View()
	cases := []struct {
		name string
		sets map[string]int64
		want error
	}{
		{"unknown key", map[string]int64{"zz": 1}, ErrUnknownNode},
		{"derived key", map[string]int64{"D": 1}, ErrNotSource},
		{"mixed batch", map[string]int64{"A": 9, "zz": 1}, ErrUnknownNode},
		{"derived in batch", map[string]int64{"A": 9, "B": 1}, ErrNotSource},
	}
	for _, tc := range cases {
		if _, err := a.Apply(tc.sets); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
		if !maps.Equal(before, a.View()) {
			t.Errorf("%s: rejected apply changed state", tc.name)
		}
	}
	if _, err := a.Apply(map[string]int64{"A": 9}); err != nil {
		t.Errorf("valid apply after rejections must work: %v", err)
	}
}

func TestSevenLogs(t *testing.T) {
	a, _ := New(seven)
	log1, _ := a.Apply(map[string]int64{"A": 5})
	want1 := []Change{
		{Name: "A", Old: 1, New: 5}, {Name: "B", Old: 2, New: 10}, {Name: "C", Old: 11, New: 15},
		{Name: "D", Old: 13, New: 25}, {Name: "F", Old: 113, New: 125}, {Name: "G", Old: 14, New: 30},
	}
	if !reflect.DeepEqual(log1, want1) {
		t.Errorf("Apply{A:5} = %v, want %v", log1, want1)
	}
	log2, _ := a.Apply(map[string]int64{"A": 0, "E": 10})
	want2 := []Change{
		{Name: "A", Old: 5, New: 0}, {Name: "E", Old: 100, New: 10}, {Name: "B", Old: 10, New: 0},
		{Name: "C", Old: 15, New: 10}, {Name: "D", Old: 25, New: 10}, {Name: "F", Old: 125, New: 20},
		{Name: "G", Old: 30, New: 10},
	}
	if !reflect.DeepEqual(log2, want2) {
		t.Errorf("Apply{A:0,E:10} = %v, want %v", log2, want2)
	}
}

func TestNoopApply(t *testing.T) {
	a, _ := New(seven)
	for _, sets := range []map[string]int64{{"A": 1}, {"A": 1, "E": 100}, {}} {
		if log, err := a.Apply(sets); err != nil || len(log) != 0 {
			t.Errorf("Apply%v = %v, %v; want empty log", sets, log, err)
		}
	}
}

func TestSelfCheckConcurrent(t *testing.T) {
	a, err := New(seven)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SelfCheck(); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				if err := a.SelfCheck(); err != nil {
					errs <- err
					return
				}
				if _, ok := a.Value("D"); !ok {
					errs <- errors.New("Value(D) missing")
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			if _, err := a.Apply(map[string]int64{"A": int64(i % 5)}); err != nil {
				errs <- err
				return
			}
		}
	}()
	wg.Wait()
	select {
	case err := <-errs:
		t.Fatal(err)
	default:
	}
}
