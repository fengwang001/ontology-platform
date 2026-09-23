package main

import (
	"errors"
	"fmt"

	"ontology/graph"
)

var pass, fail int

func report(name string, ok bool) {
	if ok {
		pass++
		fmt.Printf("OK   %s\n", name)
	} else {
		fail++
		fmt.Printf("FAIL %s\n", name)
	}
}

func main() {
	// 1. 拓扑分层并发执行：中层宽度为 2。
	g, err := graph.New([]string{"a", "b", "c", "d"},
		[][2]string{{"a", "c"}, {"a", "d"}, {"b", "c"}, {"b", "d"}})
	ls := g.Layers()
	report("layered scheduling (width 2)", err == nil && len(ls) == 2 && len(ls[1]) == 2)

	// 2. 环被拒并报真实环路径。
	_, err = graph.New([]string{"a", "b", "c"}, [][2]string{{"a", "b"}, {"b", "c"}, {"c", "a"}})
	var ce *graph.CycleError
	closed := errors.As(err, &ce) && len(ce.Path) >= 2 && ce.Path[0] == ce.Path[len(ce.Path)-1]
	report("cycle rejected with closed path", closed)

	_ = pass
	_ = fail
	fmt.Printf("TOTAL %d/%d\n", pass, pass+fail)
}
