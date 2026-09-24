package plan_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/name"
	"ontology/plan"
)

func steps(pl *plan.Plan) []plan.Step { return pl.Chains }

func runNoClobber(t *testing.T, init []string, st []plan.Step) {
	t.Helper()
	sp := name.New(init...)
	for _, s := range st {
		if sp.Has(s.To) {
			t.Fatalf("before %v→%v target already exists (clobber)", s.From, s.To)
		}
		if err := sp.Move(s.From, s.To); err != nil {
			t.Fatalf("move %v→%v failed: %v", s.From, s.To, err)
		}
	}
}

func TestOrderAndCycles(t *testing.T) {
	cases := []struct {
		name     string
		init     []string
		reqs     []plan.Req
		want     []plan.Step
		cycSizes []int
	}{
		{"empty", nil, nil, nil, nil},
		{"single", []string{"a"}, []plan.Req{{"a", "b"}},
			[]plan.Step{{"a", "b"}}, nil},
		{"chain", []string{"a", "b"}, []plan.Req{{"a", "b"}, {"b", "c"}},
			[]plan.Step{{"b", "c"}, {"a", "b"}}, nil},
		{"two chains sorted", []string{"a", "c"},
			[]plan.Req{{"a", "b"}, {"c", "d"}},
			[]plan.Step{{"a", "b"}, {"c", "d"}}, nil},
		{"self loop no-op", []string{"a"}, []plan.Req{{"a", "a"}}, nil, nil},
		{"2-cycle", []string{"a", "b"},
			[]plan.Req{{"a", "b"}, {"b", "a"}}, nil, []int{2}},
		{"3-cycle", []string{"a", "b", "c"},
			[]plan.Req{{"a", "b"}, {"b", "c"}, {"c", "a"}}, nil, []int{3}},
		{"chain plus cycle", []string{"a", "b", "x", "y"},
			[]plan.Req{{"a", "b"}, {"b", "c"}, {"x", "y"}, {"y", "x"}},
			[]plan.Step{{"b", "c"}, {"a", "b"}}, []int{2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pl, err := plan.Compile(tc.reqs, tc.init)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			if !reflect.DeepEqual(steps(pl), tc.want) {
				t.Fatalf("steps = %v, want %v", steps(pl), tc.want)
			}
			if len(pl.Cycles) != len(tc.cycSizes) {
				t.Fatalf("cycles = %d, want %d", len(pl.Cycles), len(tc.cycSizes))
			}
			for i, sz := range tc.cycSizes {
				if len(pl.Cycles[i].Reqs) != sz {
					t.Fatalf("cycle %d size = %d, want %d", i, len(pl.Cycles[i].Reqs), sz)
				}
			}
			runNoClobber(t, tc.init, pl.Chains)
		})
	}
}

func TestConflicts(t *testing.T) {
	cases := []struct {
		name string
		init []string
		reqs []plan.Req
		kind error
	}{
		{"dest exists", []string{"a", "b"}, []plan.Req{{"a", "b"}}, plan.ErrDestExists},
		{"dup dest", []string{"a", "b"}, []plan.Req{{"a", "x"}, {"b", "x"}}, plan.ErrDupDest},
		{"dup src", []string{"a"}, []plan.Req{{"a", "x"}, {"a", "y"}}, plan.ErrDupSrc},
		{"src missing", []string{"a"}, []plan.Req{{"z", "x"}}, plan.ErrSrcMissing},
		{"many dup src caught", []string{"a"},
			[]plan.Req{{"a", "1"}, {"a", "2"}, {"a", "3"}}, plan.ErrDupSrc},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := name.New(tc.init...).Snapshot()
			sp := name.New(tc.init...)
			_, err := plan.Compile(tc.reqs, sp.Snapshot())
			if !errors.Is(err, tc.kind) {
				t.Fatalf("err = %v, want kind %v", err, tc.kind)
			}
			if got := sp.Snapshot(); !reflect.DeepEqual(got, before) {
				t.Fatalf("namespace changed on conflict: %v vs %v", got, before)
			}
		})
	}
}

func TestComplexity(t *testing.T) {
	const n = 50000
	var reqs []plan.Req
	var init []string
	for i := 0; i < n; i++ {
		a := fmt.Sprintf("n%06d", i)
		b := fmt.Sprintf("m%06d", i)
		init = append(init, a)
		reqs = append(reqs, plan.Req{a, b})
	}
	pl, err := plan.Compile(reqs, init)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	bound := 4 * (len(reqs) + len(init))
	if pl.Lookups() > bound {
		t.Fatalf("lookups = %d > bound %d", pl.Lookups(), bound)
	}
}

func TestDeterminism(t *testing.T) {
	reqs := []plan.Req{{"a", "b"}, {"b", "c"}, {"d", "e"}, {"e", "f"}, {"g", "h"}}
	init := []string{"a", "b", "d", "e", "g"}
	var ref []plan.Step
	r := rand.New(rand.NewSource(1))
	for iter := 0; iter < 20; iter++ {
		sh := append([]plan.Req{}, reqs...)
		r.Shuffle(len(sh), func(i, j int) { sh[i], sh[j] = sh[j], sh[i] })
		pl, err := plan.Compile(sh, init)
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		if ref == nil {
			ref = pl.Chains
			continue
		}
		if !reflect.DeepEqual(pl.Chains, ref) {
			t.Fatalf("iter %d: %v != %v", iter, pl.Chains, ref)
		}
	}
}

func TestNameSpace(t *testing.T) {
	validCases := []string{"", "a", "dir/file", "/abs/path", "x:y?z"}
	for _, n := range validCases {
		if !name.Valid(n) {
			t.Errorf("Valid(%q) = false", n)
		}
	}
	moveCases := []struct {
		name   string
		init   []string
		from   string
		to     string
		want   []string
		wantEr error
	}{
		{"simple", []string{"a"}, "a", "b", []string{"b"}, nil},
		{"missing source", []string{"a"}, "z", "b", []string{"a"}, name.ErrMissing},
		{"dest exists", []string{"a", "b"}, "a", "b", []string{"a", "b"}, name.ErrExists},
		{"self loop no-op", []string{"a"}, "a", "a", []string{"a"}, nil},
		{"empty name", []string{""}, "", "x", []string{"x"}, nil},
		{"slash ordinary", []string{"a/b"}, "a/b", "c/d", []string{"c/d"}, nil},
	}
	for _, tc := range moveCases {
		t.Run(tc.name, func(t *testing.T) {
			sp := name.New(tc.init...)
			err := sp.Move(tc.from, tc.to)
			if !errors.Is(err, tc.wantEr) {
				t.Fatalf("err = %v, want %v", err, tc.wantEr)
			}
			if got := sp.Snapshot(); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("state = %v, want %v", got, tc.want)
			}
		})
	}
	// 持写锁期间并发 Move 被阻塞，释放后完成。
	sp := name.New("a")
	sp.Lock()
	acq := make(chan bool, 1)
	go func() { _ = sp.Move("a", "b"); acq <- true }()
	select {
	case <-acq:
		t.Fatal("Move should block while write lock is held")
	default:
	}
	sp.Unlock()
	<-acq
	if !sp.Has("b") {
		t.Fatal("Move should complete after unlock")
	}
}
