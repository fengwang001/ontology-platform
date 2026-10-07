package ontology

import "testing"

// TestCheckCountIndependentOfTotalScale 以可观测的 Report.Checks 证明：
// 当系统中实例/链接总量增长、而动作声明与实际触及集合不变时，
// 权限检查次数保持不变——开销只与该动作声明/触及的对象相关。
func TestCheckCountIndependentOfTotalScale(t *testing.T) {
	makeWorld := func(extra int) (*MemStore, *testDecider) {
		insts := []Instance{inst("A", "doc"), inst("B", "doc"), inst("C", "doc")}
		edges := []Edge{edge("rel", "A", "B"), edge("rel", "B", "C")}
		for i := 0; i < extra; i++ {
			id := InstanceID("x" + itoa(i))
			insts = append(insts, Instance{ID: id, Type: "noise", Version: 1, Attrs: map[string]string{}})
			edges = append(edges, edge("rel", string(id), "x"+itoa((i+1)%max1(extra))))
		}
		d := newTestDecider()
		for _, id := range []string{"A", "B", "C"} {
			d.allowAll(InstanceID(id), OpUpdate)
		}
		for i := 0; i < extra; i++ {
			d.allowAll(InstanceID("x"+itoa(i)), OpUpdate)
		}
		return mkStore(insts, edges), d
	}

	var baseline int
	for _, extra := range []int{0, 100, 1000, 10000} {
		st, d := makeWorld(extra)
		eng := NewEngine(st, d, nil)
		rep, err := eng.Execute(baseAction("A"))
		mustCommit(t, rep, err)
		if baseline == 0 {
			baseline = rep.Checks
			continue
		}
		if rep.Checks != baseline {
			t.Fatalf("checks=%d at extra=%d, baseline=%d: check count scales with total size",
				rep.Checks, extra, baseline)
		}
	}
}

// TestCheckCountScalesOnlyWithTouchedSet 证明检查次数随实际触及实例数线性变化，
// 而不随类型/链接类型数量变化。
func TestCheckCountScalesOnlyWithTouchedSet(t *testing.T) {
	prev := 0
	for _, maxDepth := range []int{0, 1, 2, 3, 4} {
		var insts []Instance
		var edges []Edge
		for i := 0; i <= maxDepth; i++ {
			id := InstanceID("n" + itoa(i))
			insts = append(insts, inst(string(id), "doc"))
			if i > 0 {
				edges = append(edges, edge("rel", "n"+itoa(i-1), string(id)))
			}
		}
		st2, d2 := mkStore(insts, edges), newTestDecider()
		for _, in := range insts {
			d2.allowAll(in.ID, OpUpdate)
		}
		eng2 := NewEngine(st2, d2, nil)
		a := baseAction("n0")
		a.MaxDepth = maxDepth
		rep, err := eng2.Execute(a)
		mustCommit(t, rep, err)
		touched := maxDepth + 1
		// 单层票模型：每个被触及实例 1 次可见性 + 1 次授权。
		want := 2 * touched
		if rep.Checks != want {
			t.Fatalf("depth=%d touched=%d checks=%d want %d", maxDepth, touched, rep.Checks, want)
		}
		if prev != 0 && rep.Checks <= prev {
			t.Fatalf("checks should grow as touched set grows")
		}
		prev = rep.Checks
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		b[p] = '-'
	}
	return string(b[p:])
}

func max1(n int) int {
	if n <= 1 {
		return 1
	}
	return n
}
