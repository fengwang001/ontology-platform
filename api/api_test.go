package api

import (
	"sync"
	"testing"

	"ontology/ast"
)

func TestSelfCheck(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// N 个 goroutine 并发对同一批 AST 做 RoundTrip，结果必须结构相等；不用 sleep。
func TestConcurrentRoundTrip(t *testing.T) {
	trees := []*ast.Expr{
		ast.BinOp("+", ast.IntLit(258), ast.Var("ab")),
		ast.IntLit(4294967296),
		ast.Var("a\x00b"),
		ast.Var(""),
		ast.If(ast.BinOp("<=", ast.Var("n"), ast.IntLit(10)),
			ast.Neg(ast.Var("n")), ast.BinOp("*", ast.Var("n"), ast.IntLit(2))),
		ast.Not(ast.BinOp("||", ast.BoolLit(true), ast.Var("x"))),
	}
	const workers = 32
	var wg sync.WaitGroup
	errs := make(chan string, workers*len(trees))
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i, n := range trees {
				got, err := RoundTrip(n)
				if err != nil {
					errs <- "decode error"
					continue
				}
				if !ast.Equal(got, n) {
					errs <- "not equal"
				}
				_ = i
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}
