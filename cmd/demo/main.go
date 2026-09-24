package main

import (
	"fmt"

	"ontology/graph"
	"ontology/sched"
)

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{}

	// [graph] 环检测：构造含自环与环的图，逐边验证环路径真实且闭合。
	g := mkGraph([]string{"a", "b", "c"},
		[][2]string{{"a", "b"}, {"b", "c"}, {"c", "b"}})
	cyc := g.Cycle()
	cycOK := len(cyc) >= 2 && cyc[0] == cyc[len(cyc)-1]
	for i := 0; cycOK && i+1 < len(cyc); i++ {
		cycOK = g.HasEdge(cyc[i], cyc[i+1])
	}
	self := mkGraph([]string{"s"}, [][2]string{{"s", "s"}})
	cycOK = cycOK && len(self.Cycle()) == 2
	checks = append(checks, check{"cycle-path-verified-edge-by-edge", cycOK})

	// [sched] 500 个无依赖任务、上限 8：峰值不得超过 8；就绪判定 ≤ 4*(n+e)。
	ig := graph.New()
	for i := 0; i < 500; i++ {
		ig.Add(fmt.Sprintf("t%03d", i))
	}
	sc := sched.New(ig, 8)
	var running []string
	done := 0
	for done < 500 {
		for {
			id := sc.Next()
			if id == "" {
				break
			}
			running = append(running, id)
		}
		sc.Done(running[0])
		running = running[1:]
		done++
	}
	checks = append(checks, check{"concurrency-peak<=8", sc.Peak() <= 8 && sc.Peak() == 8})
	checks = append(checks, check{"ready-decisions<=4(n+e)", sc.Decisions() <= 4*500})

	printChecks(checks)
}

func printChecks(checks []check) {
	pass := 0
	for _, c := range checks {
		tag := "FAIL"
		if c.ok {
			tag, pass = "OK", pass+1
		}
		fmt.Printf("%s %s\n", tag, c.name)
	}
	fmt.Printf("TOTAL: %d/%d OK\n", pass, len(checks))
}

func mkGraph(nodes []string, edges [][2]string) *graph.Graph {
	g := graph.New()
	for _, n := range nodes {
		g.Add(n)
	}
	for _, e := range edges {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			panic(err)
		}
	}
	return g
}
