package main

import (
	"errors"
	"fmt"

	"ontology/graph"
	"ontology/journal"
)

// 演示程序：每实现完一个包就补一条对应判定，不读参数、不联网、退出码 0。

type check struct {
	name string
	ok   bool
}

func main() {
	var checks []check

	// graph：宽 DAG 分层，最大一层宽度为 4。
	b := graph.NewBuilder()
	b.Add("root")
	for _, id := range []graph.StepID{"a", "b", "c", "d"} {
		b.Add(id, "root")
	}
	g, err := b.Build()
	width := 0
	for _, l := range g.Layers() {
		if len(l) > width {
			width = len(l)
		}
	}
	checks = append(checks, check{"layered-concurrency width=4", err == nil && width == 4})

	// graph：环必须被拒绝，且报出首尾闭合的真实环路径。
	cb := graph.NewBuilder()
	cb.Add("a", "c")
	cb.Add("b", "a")
	cb.Add("c", "b")
	_, cerr := cb.Build()
	var cyc *graph.CycleError
	closed := false
	if errors.As(cerr, &cyc) && len(cyc.Path) >= 2 && cyc.Path[0] == cyc.Path[len(cyc.Path)-1] {
		closed = true
	}
	checks = append(checks, check{"cycle rejected with closed path", closed})

	// journal：同一字节序列重放多次结果完全相同（幂等重放）。
	st := journal.NewStore(0)
	for _, ph := range []journal.Phase{journal.Running, journal.Completed} {
		st.Append("s1", ph, "")
	}
	r1, _ := st.Replay()
	r2, _ := st.Replay()
	idem := len(r1) == 2 && len(r2) == 2
	for i := range r1 {
		idem = idem && r1[i] == r2[i]
	}
	checks = append(checks, check{"journal replay idempotent", idem})

	// journal：半条记录（截断尾部）被确定性丢弃，已提交部分仍自洽。
	half := journal.NewStore(0)
	half.Append("s1", journal.Completed, "")
	before := half.Len()
	half.AppendRaw(half.Bytes()[before-2:]) // 故意追加 2 个残缺字节
	rs, _ := half.Replay()
	checks = append(checks, check{"half record deterministically dropped", len(rs) == 1 && rs[0].StepID == "s1"})

	pass := 0
	for _, c := range checks {
		if c.ok {
			pass++
			fmt.Printf("OK   %s\n", c.name)
		} else {
			fmt.Printf("FAIL %s\n", c.name)
		}
	}
	fmt.Printf("TOTAL %d/%d OK\n", pass, len(checks))
	if pass != len(checks) {
		panic("demo checks failed")
	}
}
