package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync"

	"ontology/api"
	"ontology/wfq"
)

var fails int

func ok(name string, cond bool) {
	if !cond {
		fmt.Println("FAIL", name)
		fails++
		return
	}
	fmt.Println("OK", name)
}

func main() {
	// 第三节八步：op[0] 0=Submit/1=Dequeue，op[1]=flow，op[2]=size；逐项记 (F0,F1,V,result)。
	s, _ := wfq.New([]int{2, 1})
	ops := [][3]int{{0, 0, 2}, {0, 1, 1}, {1, 0, 0}, {1, 0, 0},
		{0, 1, 2}, {1, 0, 0}, {0, 0, 2}, {1, 0, 0}}
	var tr []string
	for _, o := range ops {
		r := "-"
		if o[0] == 1 {
			f, _ := s.Dequeue()
			r = fmt.Sprint(f)
		} else {
			_ = s.Submit(o[1], o[2])
		}
		tr = append(tr, fmt.Sprintf("(%d,%d,%d,%s)", s.FlowFinish(0), s.FlowFinish(1), s.VirtualTime(), r))
	}
	want := "[(1,0,0,-) (1,1,0,-) (1,1,1,0) (1,1,1,1) (1,3,1,-) (1,3,3,1) (4,3,3,-) (4,3,4,0)]"
	ok("8-step (F0,F1,V,result): "+fmt.Sprint(tr), fmt.Sprint(tr) == want)
	// 不变量 1：随机序列（循环生成）与朴素 O(n) 扫描（并列取小下标）返回序列一致。
	w := []int{2, 1, 3}
	n, rng := len(w), rand.New(rand.NewSource(7))
	q, _ := api.New(w)
	F := make([]int, n)
	qs := make([][]int, n)
	live, agree := 0, true
	for i := 0; i < 200 || live > 0; i++ {
		if live == 0 || (i < 200 && rng.Intn(2) == 0) {
			f, sz := rng.Intn(n), rng.Intn(9)+1
			if q.Submit(f, sz) != nil {
				agree = false
			}
			F[f] = max(F[f], q.VirtualTime()) + (sz+w[f]-1)/w[f]
			qs[f], live = append(qs[f], F[f]), live+1
			continue
		}
		g, _ := q.Dequeue()
		pick := -1
		for f := 0; f < n; f++ { // ascending scan keeps the smaller index on ties
			if len(qs[f]) > 0 && (pick < 0 || qs[f][0] < qs[pick][0]) {
				pick = f
			}
		}
		if g != pick {
			agree = false
		}
		qs[pick], live = qs[pick][1:], live-1
	}
	ok("naive reference agreement", agree)
	// 不变量 2：F 严格上升、流内完成时刻升序、Dequeue 不动 F。
	m, _ := wfq.New([]int{1, 3})
	mono, last := true, []int{0, 0}
	asc := func(x []int) bool {
		for i := 1; i < len(x); i++ {
			if x[i] <= x[i-1] {
				return false
			}
		}
		return true
	}
	for i := 0; i < 40; i++ {
		f, before := i%2, m.FlowFinish(i%2)
		_ = m.Submit(f, i+1)
		if m.FlowFinish(f) <= before || before != last[f] || !asc(m.FlowQueue(f)) {
			mono = false
		}
		last[f] = m.FlowFinish(f)
		if i%5 == 0 {
			_, _ = m.Dequeue()
		}
	}
	ok("finish monotone & in-queue ascending", mono)
	// 不变量 3 + (丙)：权重越大 inc 越小先出；同完成时刻并列取小下标。
	fr, _ := api.New([]int{3, 1})
	_ = fr.Submit(0, 3)
	_ = fr.Submit(1, 3)
	d1, _ := fr.Dequeue()
	d2, _ := fr.Dequeue()
	ft, _ := api.New([]int{2, 1})
	_ = ft.Submit(0, 1)
	_ = ft.Submit(1, 1)
	t1, _ := ft.Dequeue()
	t2, _ := ft.Dequeue()
	ok("fairness & tie->smaller index", d1 == 0 && d2 == 1 && t1 == 0 && t2 == 1)
	// 第五节：三类哨兵互不相同；坏 Dequeue 仍是 ErrEmpty（坏 Submit 没入队），随后正常工作。
	_, eCfg := api.New([]int{0})
	re, _ := api.New([]int{2, 1})
	eSub := re.Submit(9, 1)
	_, eEmp := re.Dequeue()
	ok("three distinct sentinel errors",
		errors.Is(eCfg, api.ErrConfig) && errors.Is(eSub, api.ErrSubmit) &&
			errors.Is(eEmp, api.ErrEmpty) && eCfg != eSub && eSub != eEmp && eCfg != eEmp)
	_ = re.Submit(0, 2)
	_ = re.Submit(1, 1)
	r1, _ := re.Dequeue()
	r2, _ := re.Dequeue()
	ok("rejection leaves no trace, scheduler still works", errors.Is(eEmp, api.ErrEmpty) && r1 == 0 && r2 == 1)

	// 第四节：m=100/1000/10000 时一次 Dequeue 只检查堆根 1 个流，不随 m 增长。
	ok("min-selection probes O(1) in m", s.ProbesBounded([]int{100, 1000, 10000}, 1))

	// 第六节：N 个 goroutine 各提交一包；恰好 N 个包且按升序出队（V 严格递增）。
	const N = 200
	cq, _ := api.New([]int{1})
	var wg sync.WaitGroup
	for i := 1; i <= N; i++ {
		wg.Add(1)
		go func(sz int) { defer wg.Done(); _ = cq.Submit(0, sz) }(i)
	}
	wg.Wait()
	cnt, prev, cOK := 0, 0, true
	for {
		f, err := cq.Dequeue()
		if errors.Is(err, api.ErrEmpty) {
			break
		}
		if f != 0 || cq.VirtualTime() <= prev {
			cOK = false
		}
		prev, cnt = cq.VirtualTime(), cnt+1
	}
	ok("concurrent submit: exactly N ascending", cnt == N && cOK)

	if fails > 0 {
		os.Exit(1)
	}
}
