// Command demo exercises the structured-log masking and sampling pipeline.
package main

import (
	"errors"
	"fmt"

	"ontology/record"
)

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK", name)
	} else {
		fmt.Println("FAIL", name)
		fails++
	}
}

func nested(depth int) map[string]any {
	root := map[string]any{"k": "v"}
	cur := root
	for i := 1; i < depth; i++ {
		next := map[string]any{}
		cur["k"] = next
		cur = next
	}
	cur["k"] = "v"
	return root
}

func main() {
	_, err := record.New("", false, record.LevelInfo, nested(record.MaxDepth+1))
	deepRejected := errors.Is(err, record.ErrDepth)

	cyc := map[string]any{"a": "x"}
	cyc["self"] = cyc
	_, err = record.New("", false, record.LevelInfo, cyc)
	cycleRejected := errors.Is(err, record.ErrCycle)

	dag := map[string]any{"x": "1"}
	shared := map[string]any{"k": "v"}
	dag["p"] = shared
	dag["q"] = shared
	_, err = record.New("", false, record.LevelInfo, dag)

	check("深度超限与循环引用被拒绝(DAG不误报)", deepRejected && cycleRejected && err == nil)

	fmt.Printf("TOTAL 1/%d checks OK\n", 1+fails)
	if fails > 0 {
		panic("checks failed")
	}
}
