package narrowing

import (
	"fmt"
	"sync"
	"testing"
)

func mustFail(t *testing.T, decls []Decl, body []Stmt) *AnalysisError {
	t.Helper()
	res, err := Analyze(decls, body)
	if err == nil {
		t.Fatalf("analysis must fail, got result %+v", res)
	}
	aerr, ok := err.(*AnalysisError)
	if !ok {
		t.Fatalf("error must be *AnalysisError, got %T", err)
	}
	return aerr
}

func TestErrorClasses(t *testing.T) {
	shape := ObjectExpr(Prop("k", NumLitExpr(1)))
	other := ObjectExpr(Prop("j", NumLitExpr(2)))

	cases := []struct {
		name  string
		decls []Decl
		body  []Stmt
		class ErrorClass
	}{
		{"undeclared in condition",
			[]Decl{decl("x", NumberExpr())},
			[]Stmt{&If{ID: "a", Cond: Truthy{Var: "ghost"}}},
			ErrInvalidArg},
		{"undeclared assignment target",
			[]Decl{decl("x", NumberExpr())},
			[]Stmt{&Assign{ID: "a", Var: "ghost", Type: NumberExpr()}},
			ErrInvalidArg},
		{"malformed type: duplicate property",
			[]Decl{decl("x", ObjectExpr(Prop("k", NumberExpr()), Prop("k", StringExpr())))},
			nil,
			ErrInvalidArg},
		{"duplicate statement id",
			[]Decl{decl("x", NumberExpr())},
			[]Stmt{&Assign{ID: "a", Var: "x", Type: NumberExpr()}, &Assign{ID: "a", Var: "x", Type: NumberExpr()}},
			ErrInvalidArg},
		{"property on non-object member",
			[]Decl{decl("x", UnionExpr(NumberExpr(), shape))},
			[]Stmt{&If{ID: "a", Cond: PropEq{Var: "x", Prop: "k", Lit: NumLit(1)}}},
			ErrPropNotAccessible},
		{"missing discriminant property",
			[]Decl{decl("x", UnionExpr(shape, other))},
			[]Stmt{&If{ID: "a", Cond: PropEq{Var: "x", Prop: "k", Lit: NumLit(1)}}},
			ErrMissingDiscriminant},
		{"not assignable",
			[]Decl{decl("x", NumberExpr())},
			[]Stmt{&Assign{ID: "a", Var: "x", Type: UnionExpr(NumberExpr(), StringExpr())}},
			ErrNotAssignable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			aerr := mustFail(t, tc.decls, tc.body)
			if aerr.Class != tc.class {
				t.Fatalf("got class %s, want %s (err: %v)", aerr.Class, tc.class, aerr)
			}
		})
	}
}

// Class priority dominates program order; within one class the earliest
// statement wins.
func TestErrorPriority(t *testing.T) {
	// Statement s1 is not assignable, statement s2 references an
	// undeclared variable: the invalid argument wins despite being later.
	aerr := mustFail(t,
		[]Decl{decl("x", NumberExpr())},
		[]Stmt{
			&Assign{ID: "s1", Var: "x", Type: StringExpr()},
			&If{ID: "s2", Cond: Truthy{Var: "ghost"}},
		})
	if aerr.Class != ErrInvalidArg {
		t.Fatalf("invalid argument must dominate: got %s", aerr.Class)
	}

	// Two not-assignable errors: the first in program order is reported.
	aerr = mustFail(t,
		[]Decl{decl("x", NumberExpr()), decl("y", NumberExpr())},
		[]Stmt{
			&Assign{ID: "s1", Var: "x", Type: StringExpr()},
			&Assign{ID: "s2", Var: "y", Type: StringExpr()},
		})
	if aerr.Class != ErrNotAssignable || aerr.StmtID != "s1" || aerr.Var != "x" {
		t.Fatalf("earliest same-class error must win: got %v", aerr)
	}

	// Inaccessible property beats missing discriminant.
	aerr = mustFail(t,
		[]Decl{
			decl("a", UnionExpr(NumberExpr(), ObjectExpr(Prop("k", NumLitExpr(1))))),
			decl("b", UnionExpr(ObjectExpr(Prop("k", NumLitExpr(1))), ObjectExpr(Prop("j", NumLitExpr(2))))),
		},
		[]Stmt{
			&If{ID: "s1", Cond: PropEq{Var: "b", Prop: "k", Lit: NumLit(1)}},
			&If{ID: "s2", Cond: PropEq{Var: "a", Prop: "k", Lit: NumLit(1)}},
		})
	if aerr.Class != ErrPropNotAccessible {
		t.Fatalf("inaccessible property must dominate missing discriminant: got %s", aerr.Class)
	}
}

// Flow-dependent checks are skipped in unreachable code.
func TestNoErrorsInUnreachableCode(t *testing.T) {
	res := mustAnalyze(t,
		[]Decl{decl("x", NumLitExpr(1))},
		[]Stmt{
			&If{ID: "if", Cond: EqLiteral{Var: "x", Lit: NumLit(2)},
				Then: []Stmt{
					&Assign{ID: "bad", Var: "x", Type: StringExpr()},
					&If{ID: "badProp", Cond: PropEq{Var: "x", Prop: "k", Lit: NumLit(1)}},
				}},
		})
	if _, reachable, _ := res.Query("bad", Before, "x"); reachable {
		t.Fatal("dead branch must be unreachable")
	}
}

// Query cost is constant in program size, verified through the step
// counter; results are immutable and safe for concurrent use.
func TestQueryCostAndConcurrency(t *testing.T) {
	build := func(n int) (*Result, []StatementID) {
		decls := []Decl{decl("x", UnionExpr(NumberExpr(), StringExpr(), NullExpr()))}
		var body []Stmt
		var ids []StatementID
		for i := 0; i < n; i++ {
			id := StatementID(fmt.Sprintf("s%d", i))
			body = append(body, &If{ID: id, Cond: TypeOf{Var: "x", Kind: TypeOfNumber},
				Then: []Stmt{&Assign{ID: id + "t", Var: "x", Type: NumberExpr()}},
				Else: []Stmt{&Assign{ID: id + "e", Var: "x", Type: UnionExpr(NumberExpr(), StringExpr(), NullExpr())}}})
			ids = append(ids, id+"t")
		}
		return mustAnalyze(t, decls, body), ids
	}

	small, smallIDs := build(50)
	large, largeIDs := build(5000)

	stepsOf := func(res *Result, id StatementID) uint64 {
		before := res.QuerySteps()
		if _, _, ok := res.Query(id, Before, "x"); !ok {
			t.Fatal("query must succeed")
		}
		return res.QuerySteps() - before
	}
	smallSteps := stepsOf(small, smallIDs[len(smallIDs)-1])
	largeSteps := stepsOf(large, largeIDs[len(largeIDs)-1])
	if smallSteps != largeSteps {
		t.Fatalf("query steps must not grow with program size: %d vs %d", smallSteps, largeSteps)
	}
	t.Logf("query steps: small program=%d, large program=%d (constant as required)", smallSteps, largeSteps)

	// Concurrent queries on one result and concurrent independent
	// analyses; run with -race to verify.
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				id := largeIDs[(g*97+i)%len(largeIDs)]
				if _, _, ok := large.Query(id, Before, "x"); !ok {
					t.Error("concurrent query failed")
					return
				}
			}
			// Independent analyses must behave like some serial order.
			res, err := Analyze(
				[]Decl{decl("v", UnionExpr(NumberExpr(), NullExpr()))},
				[]Stmt{&If{ID: StatementID(fmt.Sprintf("g%d", g)), Cond: Truthy{Var: "v"}}})
			if err != nil {
				t.Error(err)
				return
			}
			if _, _, ok := res.Query("g"+StatementID(fmt.Sprint(g)), After, "v"); !ok {
				t.Error("independent analysis query failed")
			}
		}(g)
	}
	wg.Wait()
}
