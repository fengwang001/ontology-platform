package main

import (
	"errors"
	"fmt"
	"os"

	"ontology"
)

var checks, passes int

func report(ok bool, format string, args ...any) {
	checks++
	status := "FAIL"
	if ok {
		status = "OK"
		passes++
	}
	fmt.Printf("%s %s\n", status, fmt.Sprintf(format, args...))
}

func nodeID(i int) string { return fmt.Sprintf("node-%d", i) }

func build(nodes, vnodes int) *ontology.Ring {
	r := ontology.New()
	for i := 0; i < nodes; i++ {
		if err := r.Add(nodeID(i), vnodes); err != nil {
			panic(err)
		}
	}
	return r
}

func owners(r *ontology.Ring, keys []string) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		owner, err := r.Locate(k)
		if err != nil {
			panic(err)
		}
		out[i] = owner
	}
	return out
}

func maxMinRatio(r *ontology.Ring, keys []string) float64 {
	dist := map[string]int{}
	for _, o := range owners(r, keys) {
		dist[o]++
	}
	max, min := 0, -1
	for _, c := range dist {
		if c > max {
			max = c
		}
		if min < 0 || c < min {
			min = c
		}
	}
	return float64(max) / float64(min)
}

func main() {
	keys := ontology.GenerateKeys(100000)

	r10 := build(10, 200)
	ratio := maxMinRatio(r10, keys)
	report(ratio <= 4.0, "10节点x200vnode 分布最大/最小比 = %.3f (要求每节点在[0.5/10, 2/10]内)", ratio)

	before := owners(r10, keys)
	if err := r10.Add(nodeID(10), 200); err != nil {
		panic(err)
	}
	after := owners(r10, keys)
	moved := 0
	for i := range keys {
		if before[i] != after[i] {
			moved++
		}
	}
	mr := float64(moved) / float64(len(keys))
	report(mr >= 1.0/22 && mr <= 3.0/22, "加第11节点搬迁比例 = %.4f (理论~1/11=%.4f, 区间[1/22, 3/22])", mr, 1.0/11)

	victim := nodeID(3)
	before = owners(r10, keys)
	if err := r10.Remove(victim); err != nil {
		panic(err)
	}
	after = owners(r10, keys)
	unaffected, broken := 0, 0
	for i := range keys {
		if before[i] != victim {
			unaffected++
			if after[i] != before[i] {
				broken++
			}
		}
	}
	report(broken == 0, "删除%s后未受影响key = %d 个, 归属逐个不变 (异常 %d 个)", victim, unaffected, broken)

	wrap := ontology.New(ontology.WithHash(func(b []byte) uint64 {
		switch string(b) {
		case "n1#0":
			return 100
		case "n2#0":
			return 200
		case "before":
			return 50
		case "between":
			return 150
		case "after":
			return 300
		}
		return 0
	}))
	if err := wrap.Add("n1", 1); err != nil {
		panic(err)
	}
	if err := wrap.Add("n2", 1); err != nil {
		panic(err)
	}
	for _, c := range []struct{ key, want string }{
		{"before", "n1"}, {"between", "n2"}, {"after", "n1"},
	} {
		got, err := wrap.Locate(c.key)
		report(err == nil && got == c.want, "环绕语义 key=%-7s -> %s (期望 %s)", c.key, got, c.want)
	}

	sparse := maxMinRatio(build(10, 1), keys)
	dense := maxMinRatio(build(10, 200), keys)
	report(sparse > dense, "均衡度对比 vnodes=1 最大/最小比 %.2f > vnodes=200 的 %.2f", sparse, dense)

	_, err := ontology.New().Locate("k")
	report(errors.Is(err, ontology.ErrEmptyRing), "空环 Locate 返回 ErrEmptyRing: %v", err)

	stable := build(5, 100)
	snapshot := owners(stable, keys[:10000])
	dupErr := stable.Add(nodeID(0), 100)
	unchanged := errors.Is(dupErr, ontology.ErrNodeExists)
	for i, o := range owners(stable, keys[:10000]) {
		if o != snapshot[i] {
			unchanged = false
			break
		}
	}
	report(unchanged, "重复添加报 ErrNodeExists (%v) 且环上归属逐个不变", dupErr)

	fmt.Printf("SUMMARY %d/%d OK\n", passes, checks)
	if passes != checks {
		os.Exit(1)
	}
}
