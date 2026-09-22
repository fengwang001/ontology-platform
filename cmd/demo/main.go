// 等价类合并器（并查集）演示：go run ./cmd/demo
package main

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"

	"ontology/equivalence"
)

var passed, total int

func report(name string, ok bool, detail string) {
	total++
	if ok {
		passed++
		fmt.Printf("OK   %s %s\n", name, detail)
	} else {
		fmt.Printf("FAIL %s %s\n", name, detail)
	}
}

func dump(uf *equivalence.UnionFind) string {
	var buf bytes.Buffer
	for i, class := range uf.Classes() {
		if i > 0 {
			buf.WriteByte(' ')
		}
		rep, _ := uf.Find(class[0])
		fmt.Fprintf(&buf, "%s%v", rep, class)
	}
	return buf.String()
}

func main() {
	demoRepresentatives()
	demoSmallerID()
	demoOrderIndependent()
	demoChainHops()
	demoUnknown()
	demoEquivalent()
	demoRepeatedUnion()
	fmt.Printf("总计: %d/%d 步通过\n", passed, total)
}

func demoRepresentatives() {
	uf := equivalence.New()
	for _, p := range [][2]string{{"c", "e"}, {"e", "g"}, {"a", "i"}, {"i", "o"}, {"m", "x"}} {
		_, _, _ = uf.Union(p[0], p[1])
	}
	ok := uf.ClassCount() == 3
	for _, want := range []struct{ id, rep string }{{"g", "c"}, {"o", "a"}, {"x", "m"}} {
		rep, _ := uf.Find(want.id)
		ok = ok && rep == want.rep
	}
	report("代表元=类内最小ID", ok, "类: "+dump(uf))
}

func demoSmallerID() {
	uf := equivalence.New()
	for _, p := range [][2]string{{"b", "c"}, {"c", "d"}, {"d", "e"}, {"e", "f"}} {
		_, _, _ = uf.Union(p[0], p[1])
	}
	before, _ := uf.Find("f")
	_, _, _ = uf.Union("f", "a")
	after, _ := uf.Find("b")
	report("大类后并入更小ID更新代表元", before == "b" && after == "a",
		fmt.Sprintf("并入前 rep(f)=%s, 并入 a 后 rep(b)=%s", before, after))
}

func demoOrderIndependent() {
	pairs := [][2]string{{"g", "k"}, {"a", "d"}, {"d", "g"}, {"b", "h"}, {"h", "m"}, {"c", "z"}, {"f", "t"}, {"t", "a"}}
	build := func(order []int) string {
		uf := equivalence.New()
		for _, idx := range order {
			_, _, _ = uf.Union(pairs[idx][0], pairs[idx][1])
		}
		return dump(uf)
	}
	r1 := build([]int{0, 1, 2, 3, 4, 5, 6, 7})
	shuffled := rand.New(rand.NewSource(7)).Perm(len(pairs))
	r2 := build(shuffled)
	report("打乱Union顺序结果一致", r1 == r2, "类: "+r2)
}

func demoChainHops() {
	uf := equivalence.New()
	for i := 0; i < 20000; i++ {
		_, _, _ = uf.Union(fmt.Sprintf("v%d", i), fmt.Sprintf("v%d", i+1))
	}
	uf.ResetHopCount()
	rep, _ := uf.Find("v20000")
	first := uf.HopCount()
	uf.ResetHopCount()
	const n = 10000
	for i := 0; i < n; i++ {
		_, _ = uf.Find("v20000")
	}
	avg := float64(uf.HopCount()) / n

	// 平衡式两两合并造出深度随层数增加的秩树，用于暴露「无路径压缩」。
	deep := equivalence.New()
	for i := 0; i < 20001; i++ {
		deep.Add(fmt.Sprintf("d%d", i))
	}
	for gap := 1; gap < 20001; gap *= 2 {
		for i := 0; i+gap < 20001; i += 2 * gap {
			_, _, _ = deep.Union(fmt.Sprintf("d%d", i), fmt.Sprintf("d%d", i+gap))
		}
	}
	deep.ResetHopCount()
	_, _ = deep.Find("d20000")
	deepFirst := deep.HopCount()
	deep.ResetHopCount()
	for i := 0; i < n; i++ {
		_, _ = deep.Find("d20000")
	}
	deepAvg := float64(deep.HopCount()) / n
	report("两万链Find跳数对比", rep == "v0" && avg < 3 && deepAvg < 3 && deepFirst > 1,
		fmt.Sprintf("链:首次=%d/后续均=%.2f跳; 深度树:首次=%d/后续均=%.2f跳", first, avg, deepFirst, deepAvg))
}

func demoUnknown() {
	uf := equivalence.New()
	uf.Add("a")
	_, findErr := uf.Find("ghost")
	countAfter := uf.ClassCount()
	_, _, unionErr := uf.Union("ghost", "a")
	report("未知元素: Find报错不创建/Union隐式创建",
		errors.Is(findErr, equivalence.ErrUnknownElement) && countAfter == 1 && unionErr == nil,
		fmt.Sprintf("Find错误=%v, 之后类数=%d", findErr, countAfter))
}

func demoEquivalent() {
	uf := equivalence.New()
	uf.Add("a")
	uf.Add("b")
	disconnected, errFalse := uf.Equivalent("a", "b")
	_, errUnknown := uf.Equivalent("a", "ghost")
	report("Connected: 未知错误与不连通可区分",
		errFalse == nil && !disconnected && errors.Is(errUnknown, equivalence.ErrUnknownElement),
		fmt.Sprintf("不连通=(%v,%v), 未知=(false,%v)", disconnected, errFalse, errUnknown))
}

func demoRepeatedUnion() {
	uf := equivalence.New()
	_, _, _ = uf.Union("a", "b")
	_, _, _ = uf.Union("c", "d")
	before := uf.ClassCount()
	for i := 0; i < 1_000_000; i++ {
		_, merged, _ := uf.Union("a", "b")
		if merged {
			break
		}
	}
	after := uf.ClassCount()
	rep, _ := uf.Find("b")
	report("重复Union不减类数、不改代表元", before == after && after == 2 && rep == "a",
		fmt.Sprintf("类数 %d -> %d, rep(b)=%s", before, after, rep))
}
