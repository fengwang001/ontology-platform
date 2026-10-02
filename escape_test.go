package ontology

import (
	"errors"
	"slices"
	"testing"
)

func mustRegister(t *testing.T, r *Registry, name string, k, v int, stmts []Statement) {
	t.Helper()
	if err := r.Register(name, k, v, stmts); err != nil {
		t.Fatalf("Register(%s): %v", name, err)
	}
}

func siteClass(t *testing.T, r *Registry, name string, id int) EscapeClass {
	t.Helper()
	sites, err := r.Sites(name)
	if err != nil {
		t.Fatalf("Sites(%s): %v", name, err)
	}
	for _, site := range sites {
		if site.ID == id {
			return site.Class
		}
	}
	t.Fatalf("site %d not found in %q: %#v", id, name, sites)
	return StackEscape
}

func requireSummary(t *testing.T, got Summary, ret, glob []bool, e [][]bool, fresh bool, rounds int) {
	t.Helper()
	if !slices.Equal(got.Ret, ret) || !slices.Equal(got.Glob, glob) || got.Fresh != fresh || got.Rounds != rounds {
		t.Fatalf("summary mismatch: got ret=%v glob=%v fresh=%v rounds=%d; want ret=%v glob=%v fresh=%v rounds=%d",
			got.Ret, got.Glob, got.Fresh, got.Rounds, ret, glob, fresh, rounds)
	}
	if len(got.E) != len(e) {
		t.Fatalf("E length = %d, want %d", len(got.E), len(e))
	}
	for i := range e {
		if !slices.Equal(got.E[i], e[i]) {
			t.Fatalf("E[%d] = %v, want %v", i, got.E[i], e[i])
		}
	}
}

func registerSpecificationFunctions(t *testing.T) *Registry {
	r := NewRegistry()
	mustRegister(t, r, "mk", 0, 1, []Statement{
		{Kind: NewStmt, D: 0},
		{Kind: RetStmt, S: 0},
	})
	mustRegister(t, r, "g", 1, 2, []Statement{
		{Kind: NewStmt, D: 1},
		{Kind: StoreStmt, D: 1, S: 0},
		{Kind: RetStmt, S: 1},
	})
	mustRegister(t, r, "h", 0, 2, []Statement{
		{Kind: NewStmt, D: 0},
		{Kind: CallStmt, D: 1, G: "g", Args: []int{0}},
		{Kind: GlobalStmt, S: 1},
	})
	mustRegister(t, r, "h2", 0, 1, []Statement{
		{Kind: NewStmt, D: 0},
		{Kind: CallStmt, D: -1, G: "g", Args: []int{0}},
	})
	mustRegister(t, r, "r", 2, 2, []Statement{
		{Kind: CallStmt, D: -1, G: "r", Args: []int{1, 0}},
		{Kind: GlobalStmt, S: 0},
	})
	mustRegister(t, r, "f2", 2, 2, []Statement{
		{Kind: StoreStmt, D: 0, S: 1},
	})
	mustRegister(t, r, "c", 0, 2, []Statement{
		{Kind: NewStmt, D: 0},
		{Kind: NewStmt, D: 1},
		{Kind: CallStmt, D: -1, G: "f2", Args: []int{0, 1}},
		{Kind: GlobalStmt, S: 0},
	})
	mustRegister(t, r, "p", 1, 2, []Statement{
		{Kind: NewStmt, D: 1},
		{Kind: StoreStmt, D: 0, S: 1},
	})
	return r
}

func TestSpecificationExamples(t *testing.T) {
	r := registerSpecificationFunctions(t)

	if got := siteClass(t, r, "mk", 1); got != ReturnEscape {
		t.Fatalf("mk site = %v, want return", got)
	}
	got, err := r.Summary("mk")
	if err != nil {
		t.Fatal(err)
	}
	requireSummary(t, got, nil, nil, nil, true, 1)

	if got := siteClass(t, r, "g", 2); got != ReturnEscape {
		t.Fatalf("g site = %v, want return", got)
	}
	got, err = r.Summary("g")
	if err != nil {
		t.Fatal(err)
	}
	requireSummary(t, got,
		[]bool{true}, []bool{false}, [][]bool{{false}}, true, 1)

	if got := siteClass(t, r, "h", 3); got != GlobalEscape {
		t.Fatalf("h site = %v, want global", got)
	}
	if got := siteClass(t, r, "h2", 4); got != StackEscape {
		t.Fatalf("h2 site = %v, want stack", got)
	}

	got, err = r.Summary("r")
	if err != nil {
		t.Fatal(err)
	}
	requireSummary(t, got,
		[]bool{false, false}, []bool{true, true},
		[][]bool{{false, false}, {false, false}}, false, 3)

	got, err = r.Summary("f2")
	if err != nil {
		t.Fatal(err)
	}
	requireSummary(t, got,
		[]bool{false, false}, []bool{false, false},
		[][]bool{{false, true}, {false, false}}, false, 1)

	if got := siteClass(t, r, "c", 5); got != GlobalEscape {
		t.Fatalf("c site 5 = %v, want global", got)
	}
	if got := siteClass(t, r, "c", 6); got != GlobalEscape {
		t.Fatalf("c site 6 = %v, want global", got)
	}
	if got := siteClass(t, r, "p", 7); got != ParameterEscape {
		t.Fatalf("p site = %v, want parameter", got)
	}

	got, err = r.Summary("missing")
	if !errors.Is(err, ErrFunctionNotFound) {
		t.Fatalf("missing summary error = %v, want ErrFunctionNotFound", err)
	}
	if _, err := r.Sites("missing"); !errors.Is(err, ErrFunctionNotFound) {
		t.Fatalf("missing sites error = %v, want ErrFunctionNotFound", err)
	}
}

func TestClassPriorityAndClosures(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "identity", 1, 2, []Statement{
		{Kind: RetStmt, S: 0},
	})
	mustRegister(t, r, "both", 0, 2, []Statement{
		{Kind: NewStmt, D: 0},
		{Kind: GlobalStmt, S: 0},
		{Kind: NewStmt, D: 1},
		{Kind: StoreStmt, D: 0, S: 1},
		{Kind: RetStmt, S: 0},
	})
	if got := siteClass(t, r, "both", 1); got != GlobalEscape {
		t.Fatalf("seed site = %v, want global over return", got)
	}
	if got := siteClass(t, r, "both", 2); got != GlobalEscape {
		t.Fatalf("heap-reachable site = %v, want global closure", got)
	}

	mustRegister(t, r, "retchain", 0, 3, []Statement{
		{Kind: NewStmt, D: 0},
		{Kind: NewStmt, D: 1},
		{Kind: StoreStmt, D: 0, S: 1},
		{Kind: RetStmt, S: 0},
	})
	if got := siteClass(t, r, "retchain", 3); got != ReturnEscape {
		t.Fatalf("returned site = %v", got)
	}
	if got := siteClass(t, r, "retchain", 4); got != ReturnEscape {
		t.Fatalf("return-closure site = %v, want return", got)
	}
}

func TestLoadStoreAndDiscardedCall(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "empty", 1, 1, nil)
	mustRegister(t, r, "noload", 1, 2, []Statement{
		{Kind: LoadStmt, D: 1, S: 0},
		{Kind: RetStmt, S: 1},
	})
	got, err := r.Summary("noload")
	if err != nil {
		t.Fatal(err)
	}
	if got.Fresh || slices.Contains(got.Ret, true) {
		t.Fatalf("load from untouched parameter must be empty: %#v", got)
	}

	mustRegister(t, r, "saved", 0, 3, []Statement{
		{Kind: NewStmt, D: 0},
		{Kind: NewStmt, D: 1},
		{Kind: StoreStmt, D: 0, S: 1},
		{Kind: LoadStmt, D: 2, S: 0},
		{Kind: RetStmt, S: 2},
	})
	if got := siteClass(t, r, "saved", 2); got != ReturnEscape {
		t.Fatalf("stored site = %v", got)
	}

	mustRegister(t, r, "discard", 0, 1, []Statement{
		{Kind: NewStmt, D: 0},
		{Kind: CallStmt, D: -1, G: "empty", Args: []int{0}},
	})
	if got := siteClass(t, r, "discard", 3); got != StackEscape {
		t.Fatalf("discarded caller allocation = %v", got)
	}
}

func TestCalleeFreshResultAndPropagation(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "mk", 0, 1, []Statement{
		{Kind: NewStmt, D: 0},
		{Kind: RetStmt, S: 0},
	})
	mustRegister(t, r, "globparam", 1, 1, []Statement{
		{Kind: GlobalStmt, S: 0},
	})
	mustRegister(t, r, "storeparam", 2, 2, []Statement{
		{Kind: StoreStmt, D: 0, S: 1},
	})
	mustRegister(t, r, "caller", 0, 3, []Statement{
		{Kind: NewStmt, D: 0},
		{Kind: CallStmt, D: 1, G: "mk", Args: nil},
		{Kind: GlobalStmt, S: 1},
	})
	if got := siteClass(t, r, "caller", 2); got != StackEscape {
		t.Fatalf("caller allocation = %v; X must be separate from arg/object", got)
	}

	mustRegister(t, r, "globalone", 0, 1, []Statement{
		{Kind: NewStmt, D: 0},
		{Kind: CallStmt, D: -1, G: "globparam", Args: []int{0}},
	})
	if got := siteClass(t, r, "globalone", 3); got != GlobalEscape {
		t.Fatalf("glob propagation site = %v", got)
	}

	mustRegister(t, r, "edge", 0, 2, []Statement{
		{Kind: NewStmt, D: 0},
		{Kind: NewStmt, D: 1},
		{Kind: CallStmt, D: -1, G: "storeparam", Args: []int{0, 1}},
		{Kind: GlobalStmt, S: 0},
	})
	if got := siteClass(t, r, "edge", 4); got != GlobalEscape || siteClass(t, r, "edge", 5) != GlobalEscape {
		t.Fatalf("E propagation classes = %v %v, want global/global",
			siteClass(t, r, "edge", 4), siteClass(t, r, "edge", 5))
	}
}

func TestStatementReordering(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "a", 0, 3, []Statement{
		{Kind: NewStmt, D: 0},
		{Kind: NewStmt, D: 1},
		{Kind: StoreStmt, D: 0, S: 1},
		{Kind: LoadStmt, D: 2, S: 0},
		{Kind: RetStmt, S: 2},
	})
	mustRegister(t, r, "b", 0, 3, []Statement{
		{Kind: NewStmt, D: 0},
		{Kind: NewStmt, D: 1},
		{Kind: RetStmt, S: 2},
		{Kind: LoadStmt, D: 2, S: 0},
		{Kind: StoreStmt, D: 0, S: 1},
	})
	sa, err := r.Sites("a")
	if err != nil {
		t.Fatal(err)
	}
	sb, err := r.Sites("b")
	if err != nil {
		t.Fatal(err)
	}
	if len(sa) != len(sb) {
		t.Fatalf("reordered site count differ: %#v vs %#v", sa, sb)
	}
	for i := range sa {
		if sa[i].Class != sb[i].Class {
			t.Fatalf("reordered classes differ: %#v vs %#v", sa, sb)
		}
	}
}
