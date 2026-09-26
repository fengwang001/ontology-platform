// Command demo exercises the constant folder and prints OK/FAIL per claim.
// It takes no arguments, performs no network access, and exits 0 on success.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/ast"
	"ontology/fold"
)

func fail(format string, a ...any) {
	fmt.Printf("FAIL: "+format+"\n", a...)
	os.Exit(1)
}

// foldErr reports the sentinel error fold.Fold panics with, if any.
func foldErr(n *ast.Expr) (err error) {
	defer func() {
		if e, ok := recover().(error); ok {
			err = e
		}
	}()
	fold.Fold(n)
	return nil
}

func sumTree(m int) *ast.Expr {
	n := ast.Int(1)
	for i := 1; i < m; i++ {
		n = ast.Bin("+", n, ast.Int(1))
	}
	return n
}

func main() {
	x, y := ast.Var("x"), ast.Var("y")
	one, zero := ast.Int(1), ast.Int(0)
	d0 := ast.Bin("/", one, zero)
	big := ast.Bin("+", ast.Bin("+", ast.Bin("*", x, one), ast.Bin("*", y, zero)),
		ast.Bin("*", ast.Int(3), ast.Int(4)))

	// The six derivation steps of NOTES.md, folded one at a time.
	steps := []*ast.Expr{
		ast.Bin("*", x, one),
		ast.Bin("*", y, zero),
		ast.Bin("+", ast.Bin("*", x, one), ast.Bin("*", y, zero)),
		ast.Bin("*", ast.Int(3), ast.Int(4)),
		big,
		big,
	}
	want := []string{"x", "0", "x", "12", "(x + 12)", "(x + 12)"}
	var got []string
	for _, s := range steps {
		got = append(got, api.FoldExpr(s).String())
	}
	for i := range want {
		if got[i] != want[i] {
			fail("step %d: got %s want %s", i+1, got[i], want[i])
		}
	}
	fmt.Println("OK six steps:", got)

	if r := api.FoldExpr(big); r.String() != "(x + 12)" {
		fail("big expr -> %s", r)
	}
	fmt.Println("OK x*1+y*0+3*4 ->", api.FoldExpr(big))

	if r := api.FoldExpr(d0); !ast.Equal(r, d0) {
		fail("1/0 folded to %s", r)
	}
	if r := api.FoldExpr(ast.Bin("&&", ast.Bool(false), ast.Bin(">", d0, zero))); !ast.Equal(r, ast.Bool(false)) {
		fail("short circuit -> %s", r)
	}
	if r := api.FoldExpr(ast.Bin("*", d0, zero)); !ast.Equal(r, ast.Bin("*", d0, zero)) {
		fail("(1/0)*0 folded to %s", r)
	}
	fmt.Println("OK 1/0 kept; false&&(1/0>0) -> false; (1/0)*0 kept:", api.FoldExpr(d0),
		api.FoldExpr(ast.Bin("&&", ast.Bool(false), ast.Bin(">", d0, zero))),
		api.FoldExpr(ast.Bin("*", d0, zero)))

	if r := api.FoldExpr(ast.Bin("||", ast.Bool(true), x)); !ast.Equal(r, ast.Bool(true)) ||
		!ast.Equal(api.FoldExpr(ast.Bin("*", ast.Bin("+", ast.Int(2), ast.Int(3)), ast.Int(4))), ast.Int(20)) {
		fail("true||x or (2+3)*4 wrong")
	}
	fmt.Println("OK true||x -> true; (2+3)*4 -> 20")

	bad := []struct {
		in   *ast.Expr
		want error
	}{{nil, api.ErrNilNode}, {ast.Bin("%", one, ast.Int(2)), api.ErrUnknownOp},
		{ast.Bin("+", one, ast.Bool(true)), api.ErrTypeMismatch}}
	for _, b := range bad {
		if err := foldErr(b.in); !errors.Is(err, b.want) {
			fail("error for %v: got %v want %v", b.in, err, b.want)
		}
	}
	if !ast.Equal(api.FoldExpr(ast.Bin("+", one, ast.Int(2))), ast.Int(3)) {
		fail("state changed after rejection")
	}
	fmt.Println("OK three distinct sentinel errors; state intact after rejection")

	const m = 10000
	in := sumTree(m)
	if r := api.FoldExpr(in); r.Kind != ast.KInt || r.I != m || !ast.Equal(in, sumTree(m)) {
		fail("large-m fold wrong")
	}
	fmt.Println("OK large-m fold correct & input untouched at m =", m, "->", api.FoldExpr(sumTree(m)), "(single-pass pinned by fold.TestSinglePass)")

	batch := []*ast.Expr{big, d0, ast.Bin("*", d0, zero), sumTree(500)}
	const n = 16
	res := make([][]*ast.Expr, n)
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for _, b := range batch {
				res[g] = append(res[g], api.FoldExpr(b))
			}
		}(g)
	}
	wg.Wait()
	for g := 1; g < n; g++ {
		for k := range batch {
			if !ast.Equal(res[0][k], res[g][k]) {
				fail("concurrent goroutine %d tree %d differs", g, k)
			}
		}
	}
	fmt.Println("OK concurrent folds structurally equal")

	if err := api.SelfCheck(); err != nil {
		fail("SelfCheck: %v", err)
	}
}
