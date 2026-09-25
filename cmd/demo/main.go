// Command demo 验证 ontology 的 DRF 两资源分配器；不读参数、不联网，退出码 0。
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync"

	"ontology/alloc"
	"ontology/api"
	"ontology/drf"
)

var failures int

func ok(cond bool, msg string) {
	if cond {
		fmt.Println("OK: " + msg)
	} else {
		fmt.Println("FAIL: " + msg)
		failures++
	}
}
func fr(n, d int64) drf.Frac { return drf.Ratio(n, d) }

// naiveRef 独立朴素参照：首轮份额同步抬到先耗尽约束，s=min(C/Σ req/r)。
func naiveRef(capC, capM int64, ts []alloc.Task) map[string]drf.Frac {
	bC, bM, rs := fr(0, 1), fr(0, 1), map[string]drf.Frac{}
	for _, t := range ts {
		r := drf.DominantRatio(t.CPU, t.Mem, capC, capM)
		rs[t.ID] = r
		bC = drf.Add(bC, drf.Div(fr(t.CPU, 1), r))
		bM = drf.Add(bM, drf.Div(fr(t.Mem, 1), r))
	}
	s := drf.Div(fr(capC, 1), bC)
	if dM := drf.Div(fr(capM, 1), bM); drf.Cmp(dM, s) < 0 {
		s = dM
	}
	out := map[string]drf.Frac{}
	for id, r := range rs {
		out[id] = drf.Div(s, r)
	}
	return out
}

// scaleChanged 复刻白盒复杂度场景：m 个在位任务首轮 cpu 绑定后加 k 个纯 mem 新任务，
// 返回旧任务中结果变化的数量（应为 0：旧任务没被考察）。
func scaleChanged(m, k int) int {
	a, _ := api.New(120, 120)
	n, n1 := m-k, (m-k)/2+1
	for i := 0; i < n; i++ {
		if i < n1 {
			_ = a.Add(fmt.Sprintf("t%d", i), 4, 2)
		} else {
			_ = a.Add(fmt.Sprintf("t%d", i), 2, 4)
		}
	}
	before := a.Allocate()
	for j := 0; j < k; j++ {
		_ = a.Add(fmt.Sprintf("new%d", j), 0, 1)
	}
	after, changed := a.Allocate(), 0
	for id, v := range before {
		if drf.Cmp(after[id], v) != 0 {
			changed++
		}
	}
	return changed
}

func main() {
	rA := drf.DominantRatio(1, 6, 60, 60)
	rB := drf.DominantRatio(4, 1, 60, 60)
	ok(drf.Dominant(1, 6, 60, 60) == drf.Mem && drf.Dominant(4, 1, 60, 60) == drf.CPU &&
		drf.Cmp(rA, fr(1, 10)) == 0 && drf.Cmp(rB, fr(1, 15)) == 0 &&
		drf.Dominant(1, 1, 60, 60) == drf.CPU, "drf: 主导资源/比例判定正确（相等取CPU）")

	p := alloc.New(60, 60)
	p.Add(alloc.Task{ID: "A", CPU: 1, Mem: 6})
	p.Add(alloc.Task{ID: "B", CPU: 4, Mem: 1})
	a := p.Allocate()
	cpuUse := drf.Add(drf.Mul(a["A"], fr(1, 1)), drf.Mul(a["B"], fr(4, 1)))
	memUse := drf.Add(drf.Mul(a["A"], fr(6, 1)), drf.Mul(a["B"], fr(1, 1)))
	want := naiveRef(60, 60, []alloc.Task{{ID: "A", CPU: 1, Mem: 6}, {ID: "B", CPU: 4, Mem: 1}})
	ok(drf.Cmp(a["A"], fr(8, 1)) == 0 && drf.Cmp(a["B"], fr(12, 1)) == 0, "alloc: 四行推导成立，最终 A=8、B=12")
	ok(drf.Cmp(cpuUse, fr(56, 1)) == 0 && drf.Cmp(memUse, fr(60, 1)) == 0, "alloc: cpu 合计 56≤60、mem 合计 60 恰达上界")
	ok(drf.Cmp(drf.Mul(a["A"], rA), fr(4, 5)) == 0 && drf.Cmp(drf.Mul(a["B"], rB), fr(4, 5)) == 0, "alloc: 主导份额均为 0.8")
	ok(drf.Cmp(a["A"], want["A"]) == 0 && drf.Cmp(a["B"], want["B"]) == 0, "alloc: 与朴素逐单位推进参照逐 id 一致")

	g, _ := api.New(60, 60)
	_ = g.Add("A", 1, 6)
	before := g.Allocate()
	got := []error{g.Add("", 1, 1), g.Add("A", 1, 1), g.Add("x", -1, 1), g.Add("x", 0, 0)}
	exp := []error{api.ErrEmptyID, api.ErrDuplicateID, api.ErrNegativeDemand, api.ErrZeroDemand}
	distinct := true
	for i := range got {
		if !errors.Is(got[i], exp[i]) {
			distinct = false
		}
	}
	_, capErr := api.New(0, 1)
	ok(distinct && errors.Is(capErr, api.ErrInvalidCapacity), "api: 五类错误可判定且互不相同")
	after := g.Allocate()
	ok(len(after) == 1 && drf.Cmp(after["A"], before["A"]) == 0 && g.Add("late", 1, 1) == nil && g.SelfCheck() == nil,
		"api: 被拒后状态不变、仍可正常使用，SelfCheck 通过")

	ok(scaleChanged(1000, 3) == 0 && scaleChanged(10000, 3) == 0,
		"complexity: 大 m 下仅 k=3 个新任务被拉平（考察计数由白盒测试钉死，不随 m 增长）")

	const n = 64
	rng := rand.New(rand.NewSource(20260926))
	cg, _ := api.New(500, 500)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, idx := range rng.Perm(n) {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_ = cg.Add(fmt.Sprintf("g%d", i), int64(1+i%9), int64(1+(i*7)%11))
		}(idx)
	}
	close(start)
	wg.Wait()
	ga := cg.Allocate()
	seq, _ := api.New(500, 500)
	for i := 0; i < n; i++ {
		_ = seq.Add(fmt.Sprintf("g%d", i), int64(1+i%9), int64(1+(i*7)%11))
	}
	sa, eq, uC, uM := seq.Allocate(), len(ga) == n, fr(0, 1), fr(0, 1)
	for i := 0; i < n && eq; i++ {
		id := fmt.Sprintf("g%d", i)
		eq = drf.Cmp(ga[id], sa[id]) == 0
		uC = drf.Add(uC, drf.Mul(ga[id], fr(int64(1+i%9), 1)))
		uM = drf.Add(uM, drf.Mul(ga[id], fr(int64(1+(i*7)%11), 1)))
	}
	ok(eq && drf.Cmp(uC, fr(500, 1)) <= 0 && drf.Cmp(uM, fr(500, 1)) <= 0,
		"concurrency: 并发 Add 与顺序 Add 逐 id 一致且满足两条硬约束")

	if failures > 0 {
		os.Exit(1)
	}
}
