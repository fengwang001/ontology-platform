package main

import (
	"fmt"
	"os"

	"ontology/cgeom"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s: %s\n", name, status)
}

func main() {
	// 截断投影：C6 情形，(5,-2) 到底边 (0,0)-(4,0) 的投影被截断到端点 (4,0)，d²=5；
	// 内部投影情形，(2,5) 到顶边 (0,4)-(4,4) 投影在边内部 (2,4)，d²=1。
	d1 := cgeom.PointSegDist2(cgeom.Point{X: 5, Y: -2}, cgeom.Point{X: 0, Y: 0}, cgeom.Point{X: 4, Y: 0})
	d2 := cgeom.PointSegDist2(cgeom.Point{X: 2, Y: 5}, cgeom.Point{X: 0, Y: 4}, cgeom.Point{X: 4, Y: 4})
	check("clamped projection", d1 == (cgeom.Rat{N: 5, D: 1}) && d2 == (cgeom.Rat{N: 1, D: 1}))

	if failed {
		os.Exit(1)
	}
}
