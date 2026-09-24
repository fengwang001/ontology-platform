// demo 实际演练一致性哈希环的关键性质，每步打印一行 OK/FAIL 判定。
// 不读命令行参数、不联网；全部通过时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology"
)

const numKeys = 100_000

var failures int

func report(cond bool, format string, args ...any) {
	status := "OK"
	if !cond {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, fmt.Sprintf(format, args...))
}

func genKeys(n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = fmt.Sprintf("key-%08d", i)
	}
	return keys
}

func build(ids []string, vnodes int) *ontology.Ring {
	r := ontology.New()
	for _, id := range ids {
		if err := r.Add(id, vnodes); err != nil {
			fmt.Println("FAIL 建环失败:", err)
			os.Exit(1)
		}
	}
	return r
}

func nodeIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("node-%02d", i)
	}
	return ids
}

func locateAll(r *ontology.Ring, keys []string) []string {
	owners := make([]string, len(keys))
	for i, k := range keys {
		o, err := r.Locate(k)
		if err != nil {
			fmt.Println("FAIL Locate 出错:", err)
			os.Exit(1)
		}
		owners[i] = o
	}
	return owners
}

func maxMinRatio(owners []string) float64 {
	dist := map[string]int{}
	lo, hi := numKeys, 0
	for _, o := range owners {
		dist[o]++
	}
	for _, c := range dist {
		if c < lo {
			lo = c
		}
		if c > hi {
			hi = c
		}
	}
	return float64(hi) / float64(lo)
}

func main() {
	keys := genKeys(numKeys)
	ids := nodeIDs(10)

	// 1. 10 节点十万 key 的分布最大最小比。
	r := build(ids, 200)
	before := locateAll(r, keys)
	ratio := maxMinRatio(before)
	report(ratio <= 2.0, "分布均衡: 10 节点 x 200 虚拟节点, 最大/最小比 = %.3f (要求 <= 2.0)", ratio)

	// 2. 加第 11 个节点后的搬迁比例与理论值对比。
	_ = r.Add("node-10", 200)
	after := locateAll(r, keys)
	moved := 0
	for i := range keys {
		if before[i] != after[i] {
			moved++
		}
	}
	moveRatio := float64(moved) / numKeys
	report(moveRatio >= 1.0/22 && moveRatio <= 3.0/22,
		"加节点搬迁: %d/%d = %.4f, 理论值 1/11 = %.4f (要求落在 [1/22, 3/22])",
		moved, numKeys, moveRatio, 1.0/11)

	// 3. 删节点后未受影响 key 的数量。
	victim := "node-05"
	beforeDel := locateAll(r, keys)
	if err := r.Remove(victim); err != nil {
		report(false, "删除节点出错: %v", err)
	}
	afterDel := locateAll(r, keys)
	unaffected, violated := 0, false
	for i := range keys {
		if beforeDel[i] == victim {
			continue
		}
		if afterDel[i] != beforeDel[i] {
			violated = true
		}
		unaffected++
	}
	report(!violated, "删节点: 未受影响 key %d 个, 归属逐个不变", unaffected)

	// 4. 环绕语义三例（手工构造两虚拟节点的环）。
	h := func(s string) uint64 {
		switch s {
		case "nodeA#0":
			return 100
		case "nodeB#0":
			return 200
		case "k-before":
			return 50
		case "k-mid":
			return 150
		default:
			return 250
		}
	}
	w := ontology.New(ontology.WithHash(h))
	_ = w.Add("nodeA", 1)
	_ = w.Add("nodeB", 1)
	g1, _ := w.Locate("k-before")
	g2, _ := w.Locate("k-mid")
	g3, _ := w.Locate("k-after")
	report(g1 == "nodeA" && g2 == "nodeB" && g3 == "nodeA",
		"环绕语义: 最小前->%s, 两者之间->%s, 最大后回绕->%s", g1, g2, g3)

	// 5. vnodes=1 与 200 的均衡度对比。
	r1 := maxMinRatio(locateAll(build(ids, 1), keys))
	r200 := maxMinRatio(locateAll(build(ids, 200), keys))
	report(r1 > r200, "虚拟节点作用: vnodes=1 最大/最小比 %.1f > vnodes=200 的 %.3f", r1, r200)

	// 6. 空环错误。
	_, err := ontology.New().Locate("k")
	report(errors.Is(err, ontology.ErrEmptyRing), "空环 Locate 返回 ErrEmptyRing: %v", err)

	// 7. 重复添加错误且环不变。
	dup := build(ids[:3], 100)
	pre := locateAll(dup, keys[:10000])
	sizePre := dup.Size()
	err = dup.Add("node-01", 100)
	post := locateAll(dup, keys[:10000])
	same := dup.Size() == sizePre
	for i := range pre {
		if pre[i] != post[i] {
			same = false
		}
	}
	report(errors.Is(err, ontology.ErrNodeExists) && same,
		"重复添加: 返回 ErrNodeExists (%v), 环保持不变: %v", err, same)

	// 总计。
	total := 7
	fmt.Printf("SUMMARY %d/%d 项通过\n", total-failures, total)
	if failures > 0 {
		os.Exit(1)
	}
}
