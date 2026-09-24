package main

import (
	"fmt"

	"ontology/ast"
)

func main() {
	var fails int
	check := func(name string, ok bool, detail string) {
		tag := "OK"
		if !ok {
			tag, fails = "FAIL", fails+1
		}
		if detail != "" {
			fmt.Printf("%s %s %s\n", tag, name, detail)
		} else {
			fmt.Printf("%s %s\n", tag, name)
		}
	}

	// ast 包：三值语义——x=NULL 时 x=x 为未知而非真。
	xx := ast.CmpNode(ast.OpEq, ast.ColRef("t", "x"), ast.ColRef("t", "x"))
	check("x=x 不被折叠为真(语义:NULL→UNKNOWN)", xx.Eval(ast.Env{"t.x": {Null: true}}) == ast.Unk, "")

	if fails == 0 {
		fmt.Println("TOTAL 1/1 OK")
	} else {
		fmt.Printf("TOTAL FAIL %d\n", fails)
	}
}
