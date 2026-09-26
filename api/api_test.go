package api_test

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"ontology/api"
	"ontology/gram"
	"ontology/lr"
)

func spec(t *testing.T) *gram.Grammar {
	t.Helper()
	g, err := gram.New("S'", []gram.Symbol{"c", "d"}, []gram.Symbol{"S'", "S", "C"}, []gram.Production{
		{LHS: "S'", RHS: []gram.Symbol{"S"}},
		{LHS: "S", RHS: []gram.Symbol{"C", "C"}},
		{LHS: "C", RHS: []gram.Symbol{"c", "C"}},
		{LHS: "C", RHS: []gram.Symbol{"d"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

var seed = []lr.Item{{LHS: "S'", RHS: []gram.Symbol{"S"}, Dot: 0, Look: gram.End}}

// TestClosureOfErrors 四类故障注入各有可判定且互不相同的哨兵错误，且不返回项集。
func TestClosureOfErrors(t *testing.T) {
	g := spec(t)
	_, e1 := gram.New("S", []gram.Symbol{"a"}, []gram.Symbol{"S"},
		[]gram.Production{{LHS: "S", RHS: []gram.Symbol{"x"}}})
	_, e2 := gram.New("a", []gram.Symbol{"a"}, []gram.Symbol{"S"}, nil)
	got3, e3 := api.ClosureOf(g, []lr.Item{{LHS: "S'", RHS: []gram.Symbol{"S"}, Dot: 2, Look: gram.End}})
	got4, e4 := api.GotoOf(g, []lr.Item{{LHS: "S'", RHS: []gram.Symbol{"S"}, Look: "S"}}, "c")
	for _, tc := range []struct {
		name      string
		err, want error
	}{
		{"undeclared symbol", e1, gram.ErrUndeclaredSymbol},
		{"start not nonterminal", e2, gram.ErrStartNotNonTerm},
		{"dot out of range", e3, lr.ErrDotOutOfRange},
		{"bad lookahead", e4, lr.ErrBadLookahead},
	} {
		if !errors.Is(tc.err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, tc.err, tc.want)
		}
	}
	sents := []error{gram.ErrUndeclaredSymbol, gram.ErrStartNotNonTerm, lr.ErrDotOutOfRange, lr.ErrBadLookahead}
	for i := range sents {
		for j := i + 1; j < len(sents); j++ {
			if sents[i] == sents[j] {
				t.Errorf("sentinel %d and %d are identical", i, j)
			}
		}
	}
	if got3 != nil || got4 != nil {
		t.Error("rejected input must not return an item set")
	}
}

// TestFailureLeavesNoTrace 被拒后后续调用结果不变。
func TestFailureLeavesNoTrace(t *testing.T) {
	g := spec(t)
	before, err := api.ClosureOf(g, seed)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]lr.Item{
		{{LHS: "S'", RHS: []gram.Symbol{"S"}, Dot: 9, Look: gram.End}},
		{{LHS: "S'", RHS: []gram.Symbol{"S"}, Look: "C"}},
	} {
		if got, err := api.ClosureOf(g, bad); err == nil || got != nil {
			t.Fatalf("bad input: got %v, err %v", got, err)
		}
	}
	after, err := api.ClosureOf(g, seed)
	if err != nil || !lr.Equal(before, after) {
		t.Error("state changed after rejection")
	}
}

// TestConcurrentClosure 多 goroutine 对同一文法求同一闭包，结果逐项相同。
func TestConcurrentClosure(t *testing.T) {
	g := spec(t)
	want, err := api.ClosureOf(g, seed)
	if err != nil {
		t.Fatal(err)
	}
	const n = 32
	results := make([][]lr.Item, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, err := api.ClosureOf(g, seed)
			if err != nil {
				t.Error(err)
				return
			}
			results[i] = got
		}(i)
	}
	wg.Wait()
	for i := range results {
		if !lr.Equal(results[i], want) {
			t.Errorf("goroutine %d result differs", i)
		}
	}
}

// TestFirstSeq 表驱动核验 FIRST 序列与 ε 规则。
func TestFirstSeq(t *testing.T) {
	g, err := gram.New("S", []gram.Symbol{"a", "b"}, []gram.Symbol{"S", "A"}, []gram.Production{
		{LHS: "S", RHS: []gram.Symbol{"A", "b"}}, {LHS: "A"}, {LHS: "A", RHS: []gram.Symbol{"a"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		seq    []gram.Symbol
		follow gram.Symbol
		want   []gram.Symbol
	}{
		{"nullable head then terminal", []gram.Symbol{"A", "b"}, "$", []gram.Symbol{"a", "b"}},
		{"follow through all-nullable", []gram.Symbol{"A"}, "b", []gram.Symbol{"a", "b"}},
		{"terminal stops", []gram.Symbol{"b", "A"}, "$", []gram.Symbol{"b"}},
		{"empty seq gives follow", nil, "$", []gram.Symbol{"$"}},
	} {
		if got := g.FirstSeq(tc.seq, tc.follow); !slices.Equal(got, tc.want) {
			t.Errorf("%s: FirstSeq = %v, want %v", tc.name, got, tc.want)
		}
	}
	if !g.First("A")[gram.Eps] || g.First("a")[gram.Eps] {
		t.Error("ε membership wrong: A nullable, a not")
	}
}

// TestSelfCheck 内置文法上核验四条不变量。
func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
