package main

import (
	"errors"
	"fmt"

	"ontology/graph"
	"ontology/journal"
)

func main() {
	var pass, fail int
	check := func(name string, ok bool) {
		if ok {
			pass++
			fmt.Println("OK  ", name)
		} else {
			fail++
			fmt.Println("FAIL", name)
		}
	}

	// graph: 环被拒并报真实环路径（首尾闭合，且每条边真实存在）
	cycOK := false
	{
		g := graph.New()
		for _, n := range []string{"A", "B", "C", "D"} {
			g.AddNode(n)
		}
		_ = g.AddEdge("A", "B")
		_ = g.AddEdge("B", "C")
		_ = g.AddEdge("C", "A")
		_ = g.AddEdge("D", "A")
		_, err := g.Layers()
		var ce *graph.CycleError
		if errors.As(err, &ce) && errors.Is(err, graph.ErrCycle) {
			p := ce.Path
			cycOK = len(p) >= 3 && p[0] == p[len(p)-1]
			for i := 0; cycOK && i+1 < len(p); i++ {
				found := false
				for _, to := range g.Edges(p[i]) {
					if to == p[i+1] {
						found = true
					}
				}
				cycOK = found
			}
		}
	}
	check("环被拒并报真实环路径", cycOK)

	// journal: 重放幂等 + 半条记录被丢弃
	replayOK, tornOK := false, false
	{
		j := journal.New(nil, 0, nil)
		_, _ = j.Append("A", journal.Started, 1)
		_, _ = j.Append("A", journal.Succeeded, 1)
		raw := j.Bytes()
		r1 := journal.New(raw, 0, nil).Replay()
		r2 := journal.New(raw, 0, nil).Replay()
		replayOK = len(r1) == 2 && len(r2) == 2 && r1[1].Seq == 2
		torn := append(raw, 0x00, 0x01, 0x02, 0x03)
		jt := journal.New(torn, 0, nil)
		tr := jt.Replay()
		tornOK = len(tr) == 2 && jt.Len() == len(raw)
	}
	check("重放幂等", replayOK)
	check("半条记录被丢弃", tornOK)

	if fail > 0 {
		fmt.Printf("TOTAL %d OK, %d FAIL\n", pass, fail)
		return
	}
	fmt.Printf("TOTAL %d OK, 0 FAIL\n", pass)
}
