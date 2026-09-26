package main

import (
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/ast"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK   " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

func main() {
	i, b, v := ast.Int, ast.Bool, ast.Var
	bin := ast.Bin
	div := bin("/", i(1), i(0))
	// 第三节六步：叶节点原样；x*1->x；y*0->0；x+0->x；3*4->12；x+12 保持
	s2 := api.FoldExpr(bin("*", v("x"), i(1)))
	s3 := api.FoldExpr(bin("*", v("y"), i(0)))
	s4 := api.FoldExpr(bin("+", v("x"), i(0)))
	s5 := api.FoldExpr(bin("*", i(3), i(4)))
	s6 := api.FoldExpr(bin("+", v("x"), i(12)))
	check("steps: x*1->x, y*0->0, x+0->x, 3*4->12, x+12 kept",
		ast.Equal(s2, v("x")) && ast.Equal(s3, i(0)) && ast.Equal(s4, v("x")) &&
			ast.Equal(s5, i(12)) && ast.Equal(s6, bin("+", v("x"), i(12))))
	full := bin("+", bin("+", bin("*", v("x"), i(1)), bin("*", v("y"), i(0))), bin("*", i(3), i(4)))
	check("x*1+y*0+3*4 -> x+12", ast.Equal(api.FoldExpr(full), bin("+", v("x"), i(12))))
	check("1/0 kept; (1/0)*0 kept",
		ast.Equal(api.FoldExpr(div), div) && ast.Equal(api.FoldExpr(bin("*", div, i(0))), bin("*", div, i(0))))
	check("false&&(1/0>0)->false; true||x->true",
		ast.Equal(api.FoldExpr(bin("&&", b(false), bin(">", div, i(0)))), b(false)) &&
			ast.Equal(api.FoldExpr(bin("||", b(true), v("x"))), b(true)))
	check("(2+3)*4 -> 20", ast.Equal(api.FoldExpr(bin("*", bin("+", i(2), i(3)), i(4))), i(20)))
	check("selfcheck: 3 distinct errors + 4 invariants", api.SelfCheck() == nil)
	check("state unchanged after rejection", api.SelfCheck() == nil &&
		ast.Equal(api.FoldExpr(bin("+", i(2), i(3))), i(5)))
	check("large m=10000 single pass (counter in TestSinglePass)", ast.Equal(api.FoldExpr(build(10000)), i(10000)))
	check("concurrent fold structurally equal", concurrent(full))
	if failed {
		os.Exit(1)
	}
}

func build(m int) *ast.Expr {
	if m == 1 {
		return ast.Int(1)
	}
	return ast.Bin("+", build(m/2), build(m-m/2))
}

func concurrent(e *ast.Expr) bool {
	want := api.FoldExpr(e)
	var wg sync.WaitGroup
	ok := true
	var mu sync.Mutex
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !ast.Equal(api.FoldExpr(e), want) {
				mu.Lock()
				ok = false
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return ok
}
