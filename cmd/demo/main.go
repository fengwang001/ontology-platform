package main

import (
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/pagg"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println(name, "FAIL")
		return
	}
	fmt.Println(name, "OK")
}

func main() {
	// 1. 第三节六条记录：每步各分区部分和（按 key 升序）
	recs := []pagg.Record{
		{Partition: 0, Key: "a", Value: 5}, {Partition: 1, Key: "a", Value: 1},
		{Partition: 2, Key: "b", Value: 6}, {Partition: 0, Key: "a", Value: 3},
		{Partition: 1, Key: "c", Value: 4}, {Partition: 2, Key: "a", Value: 2},
	}
	agg := api.New(3)
	parts := [3]*pagg.Partial{pagg.New(), pagg.New(), pagg.New()}
	steps := ""
	for _, r := range recs {
		if err := agg.Add(r); err != nil {
			check("add", false)
		}
		parts[r.Partition].Add(r)
		steps += fmt.Sprintf(" P%d%v", r.Partition, parts[r.Partition].Sorted())
	}
	check("steps:"+steps, steps == " P0[{a 5}] P1[{a 1}] P2[{b 6}] P0[{a 8}] P1[{a 1} {c 4}] P2[{a 2} {b 6}]")

	// 2. Merge 后 a/b/c
	check("merge a/b/c", fmt.Sprint(agg.Merge()) == "[{a 11} {b 6} {c 4}]")

	// 3. 全序且与完成顺序无关（SelfCheck 内部枚举全部排列）
	check("selfcheck", api.SelfCheck() == nil)

	// 4+5. 三类可判定错误互不相同；被拒后状态不变
	before := fmt.Sprint(agg.Merge())
	e1 := agg.Add(pagg.Record{Partition: -1, Key: "x"})
	e2 := agg.Add(pagg.Record{Partition: 3, Key: "x"})
	e3 := agg.Add(pagg.Record{Partition: 0, Key: ""})
	check("3 sentinel errors", e1 == api.ErrPartitionNegative && e2 == api.ErrPartitionTooLarge && e3 == api.ErrEmptyKey)
	check("no trace after reject", fmt.Sprint(agg.Merge()) == before)

	// 6. 大 P 归并正确（比较次数的 O(P log P) 界由 merge 包内测试钉住；计数器非导出，此处不可读）
	const P = 10000
	big := api.New(P)
	for i := 0; i < P; i++ {
		if err := big.Add(pagg.Record{Partition: i, Key: fmt.Sprintf("k%05d", i), Value: 1}); err != nil {
			check("big add", false)
		}
	}
	check("large P merge", len(big.Merge()) == P)

	// 7. 并发 Add 后等于批量重算（64 goroutine × 100 条，k0/k1 各 15×64，k2..k6 各 14×64）
	const G = 64
	conc := api.New(G)
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = conc.Add(pagg.Record{Partition: g, Key: fmt.Sprintf("k%d", j%7), Value: 1})
			}
		}(g)
	}
	wg.Wait()
	check("concurrent == batch", fmt.Sprint(conc.Merge()) == "[{k0 960} {k1 960} {k2 896} {k3 896} {k4 896} {k5 896} {k6 896}]")

	if failed {
		os.Exit(1)
	}
}
