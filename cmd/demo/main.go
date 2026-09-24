// Command demo 演示有预算的图遍历器：逐条打印验收判定，全部通过时退出码为 0。
package main

import (
	"fmt"
	"os"
	"slices"

	"ontology/graph"
)

var failures int
var checks int

// check 打印一条判定结果并累计失败数。
func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	checks++
	fmt.Printf("%s %s\n", status, name)
}

// checkGraph 判定：邻接表按字典序读出且保留重复边。
func checkGraph() {
	g := graph.New()
	g.AddEdge("a", "c")
	g.AddEdge("a", "b")
	g.AddEdge("a", "b")
	outs, _ := g.Edges("a")
	check("邻接表出边字典序且保留重复边", slices.Equal(outs, []string{"b", "b", "c"}))
}

func main() {
	checkGraph()
	fmt.Printf("TOTAL %d checks, %d failed\n", checks, failures)
	if failures > 0 {
		os.Exit(1)
	}
}
