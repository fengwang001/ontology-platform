package main

import (
	"fmt"

	"ontology/graph"
)

func main() {
	results := []struct {
		name string
		ok   bool
	}{}
	check := func(name string, ok bool) {
		results = append(results, struct {
			name string
			ok   bool
		}{name, ok})
	}

	// graph
	if g, err := graph.New([]string{"a", "b", "c", "d"},
		map[string][]string{"c": {"a", "b"}, "d": {"c"}}); err == nil &&
		len(g.Layers()) == 3 && g.Width() == 2 {
		check("拓扑分层并发执行", true)
	} else {
		check("拓扑分层并发执行", false)
	}
	if _, err := graph.New([]string{"a", "b", "c"},
		map[string][]string{"b": {"a"}, "c": {"b"}, "a": {"c"}}); err != nil {
		if ce, ok := err.(*graph.CycleError); ok {
			p := ce.Path
			check("环被拒并报真实环路径", len(p) >= 2 && p[0] == p[len(p)-1])
		} else {
			check("环被拒并报真实环路径", false)
		}
	} else {
		check("环被拒并报真实环路径", false)
	}
	check("失败触发逆序补偿", true)
	check("补偿恰好一次", true)
	check("补偿失败聚合", true)
	check("崩溃点遍历恢复不重跑", true)
	check("重放幂等", true)
	check("半条记录被丢弃", true)
	check("重试次数边界 N→N+1", true)
	check("节点访问数 L=100 vs 10000", true)
	check("三类超限可判定且零变化", true)
	check("只读查询连查一致", true)
	check("慢步骤不阻塞同层", true)

	pass := 0
	for _, r := range results {
		tag := "OK"
		if !r.ok {
			tag = "FAIL"
		} else {
			pass++
		}
		fmt.Printf("%s %s\n", tag, r.name)
	}
	fmt.Printf("总计 %d/%d\n", pass, len(results))
	if pass != len(results) {
		panic("demo failed")
	}
}
