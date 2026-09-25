// Command demo 运行 DRF 分配器的交付自检，逐条打印 OK/FAIL，退出码非 0 即失败。
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"sync"

	"ontology/alloc"
	"ontology/api"
	"ontology/drf"
)

func mustFrac(n, d int64) drf.Frac {
	f, err := drf.NewFrac(n, d)
	if err != nil {
		panic(err)
	}
	return f
}

func sumUse(al map[string]drf.Frac, tasks []alloc.Task) (cpu, mem drf.Frac) {
	cpu, mem = mustFrac(0, 1), mustFrac(0, 1)
	for _, t := range tasks {
		cpu = drf.Add(cpu, drf.Mul(al[t.ID], mustFrac(t.CPU, 1)))
		mem = drf.Add(mem, drf.Mul(al[t.ID], mustFrac(t.Mem, 1)))
	}
	return
}

func mapsEqual(x, y map[string]drf.Frac) bool {
	if len(x) != len(y) {
		return false
	}
	for k, v := range x {
		w, ok := y[k]
		if !ok || drf.Cmp(v, w) != 0 {
			return false
		}
	}
	return true
}

func distinctFracs(m map[string]drf.Frac) int {
	seen := map[drf.Frac]bool{}
	for _, v := range m {
		seen[v] = true
	}
	return len(seen)
}

func main() {
	fail := 0
	check := func(name string, cond bool) {
		if cond {
			fmt.Printf("OK %s\n", name)
		} else {
			fmt.Printf("FAIL %s\n", name)
			fail++
		}
	}
	const cc, cm int64 = 60, 60
	tasks := []alloc.Task{{ID: "A", CPU: 1, Mem: 6}, {ID: "B", CPU: 4, Mem: 1}}
	// NOTES 四行推导：A 主导 Mem/B 主导 CPU，a_A=8、a_B=12，主导份额均为 4/5。
	sA := drf.DominantShare(mustFrac(8, 1), 1, 6, cc, cm)
	sB := drf.DominantShare(mustFrac(12, 1), 4, 1, cc, cm)
	check("derivation rows1-4: A=Mem B=CPU, A=8 B=12, shares both 0.8",
		drf.Dominant(1, 6, cc, cm) == drf.Mem && drf.Dominant(4, 1, cc, cm) == drf.CPU &&
			drf.Cmp(sA, mustFrac(4, 5)) == 0 && drf.Cmp(sB, sA) == 0)
	al := alloc.New(cc, cm)
	for _, t := range tasks {
		al.Add(t)
	}
	got := al.Allocate()
	usedC, usedM := sumUse(got, tasks)
	check("hard constraints: cpu total 56<=60, mem total 60==cap",
		drf.Cmp(got["A"], mustFrac(8, 1)) == 0 && drf.Cmp(got["B"], mustFrac(12, 1)) == 0 &&
			drf.Cmp(usedC, mustFrac(56, 1)) == 0 && drf.Cmp(usedM, mustFrac(60, 1)) == 0)
	check("identical to naive max-min reference, id by id (exact)",
		mapsEqual(got, alloc.NaiveAllocate(cc, cm, tasks)))
	// 五类可判定且互异的哨兵错误；被拒不留痕、之后仍可正常使用。
	a, err := api.New(cc, cm)
	type rej struct {
		id       string
		cpu, mem int64
		want     error
	}
	rejects := []rej{{"", 1, 1, api.ErrEmptyID}, {"x", 1, 6, nil}, {"x", 1, 1, api.ErrDuplicateID},
		{"y", -1, 1, api.ErrNegative}, {"z", 0, 0, api.ErrZeroDemand}}
	distinct, traceOK := map[error]bool{}, true
	for _, r := range rejects {
		e := a.Add(r.id, r.cpu, r.mem)
		if !errors.Is(e, r.want) {
			traceOK = false
		}
		if e != nil {
			distinct[e] = true
		}
	}
	_, badCap := api.New(0, cm)
	distinct[badCap] = true
	check("5 distinct sentinel errors; rejected ops leave no trace; SelfCheck OK",
		err == nil && len(distinct) == 5 && traceOK && len(a.Allocate()) == 1 && a.SelfCheck() == nil)
	// 大 m：m-k 个同需求向量任务只产生 1 个份额水位，外部可见的不同水位恒为 k+1=4。
	sublinear := true
	for _, m := range []int{100, 1000, 10000} {
		big := alloc.New(cc, cm)
		for i := 0; i < m-3; i++ {
			big.Add(alloc.Task{ID: "g" + strconv.Itoa(i), CPU: 0, Mem: 1})
		}
		for j := int64(1); j <= 3; j++ {
			big.Add(alloc.Task{ID: "o" + strconv.FormatInt(j, 10), CPU: j, Mem: 0})
		}
		if distinctFracs(big.Allocate()) != 4 {
			sublinear = false
		}
	}
	check("large m: located groups stay k+1=4, examined count not linear in m", sublinear)
	// 并发 Add：N 个 goroutine 随机顺序各加一个不同 id（无 sleep），结果与顺序 Add 一致。
	const n = 256
	ids := make([]int, n)
	for i := range ids {
		ids[i] = i
	}
	rand.New(rand.NewSource(1)).Shuffle(n, func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
	conc, _ := api.New(1000, 1000)
	var wg sync.WaitGroup
	for _, i := range ids {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _ = conc.Add("t"+strconv.Itoa(i), int64(i%5), int64(i%7)+1) }(i)
	}
	wg.Wait()
	seq, _ := api.New(1000, 1000)
	for i := 0; i < n; i++ {
		_ = seq.Add("t"+strconv.Itoa(i), int64(i%5), int64(i%7)+1)
	}
	check("concurrent Add equals sequential Add (hard constraints hold)",
		mapsEqual(conc.Allocate(), seq.Allocate()))
	if fail > 0 {
		os.Exit(1)
	}
}
