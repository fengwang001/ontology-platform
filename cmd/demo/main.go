package main

import (
	"fmt"

	"ontology/ast"
)

type report struct{ fail int }

func (r *report) check(name string, ok bool) {
	if ok {
		fmt.Println("OK   " + name)
	} else {
		r.fail++
		fmt.Println("FAIL " + name)
	}
}

func main() {
	r := &report{}
	t := ast.And(ast.C(ast.Eq, ast.Col{T: "t", C: 0}, ast.Col{T: "t", C: 0}), ast.K(ast.True))
	r.check("ast canonical print stable", ast.Print(t) == ast.Print(t))
	if r.fail == 0 {
		fmt.Println("TOTAL: all OK")
	} else {
		fmt.Printf("TOTAL: %d FAIL\n", r.fail)
	}
}
