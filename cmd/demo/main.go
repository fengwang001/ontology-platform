package main

import (
	"fmt"

	"ontology/graph"
)

type check struct {
	name   string
	ok     bool
	detail string
}

func main() {
	checks := []check{}

	// graph 包判定：出边字典序、重复边与自环去重。
	g := graph.New(map[string][]string{
		"b": {"a"},
		"a": {"c", "c", "a", "b"},
		"c": nil,
	})
	out, _ := g.Out("a")
	checks = append(checks, check{
		name:   "graph adjacency sorted/deduped",
		ok:     fmt.Sprint(out) == "[a b c]",
		detail: fmt.Sprint(out),
	})

	fail := 0
	for _, c := range checks {
		tag := "OK"
		if !c.ok {
			tag, fail = "FAIL", fail+1
		}
		fmt.Printf("%s %s %s\n", tag, c.name, c.detail)
	}
	fmt.Printf("TOTAL %d/%d passed\n", len(checks)-fail, len(checks))
}
