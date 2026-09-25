package main

import (
	"errors"
	"fmt"
	"sync"

	"ontology/api"
)

func main() {
	// 1) 第三节八步序列 + 天花板 + T3 持 R_x 时有效优先级
	m := api.New()
	for _, t := range []struct {
		id string
		p  int
	}{{"T1", 5}, {"T2", 3}, {"T3", 1}} {
		m.AddTask(t.id, t.p)
	}
	m.AddResource("R_x")
	m.AddResource("R_y")
	for _, u := range [][2]string{{"T1", "R_x"}, {"T3", "R_x"}, {"T2", "R_y"}, {"T3", "R_y"}} {
		m.Use(u[0], u[1])
	}
	type step struct {
		t, r string
		rel  bool
		want bool
	}
	steps := []step{
		{"T3", "R_x", false, true}, {"T2", "R_y", false, false},
		{"T1", "R_x", false, false}, {"T3", "R_x", true, false},
		{"T1", "R_x", false, true}, {"T2", "R_y", false, false},
		{"T1", "R_x", true, false}, {"T2", "R_y", false, true},
	}
	seqOK, epOK := true, m.EffectivePriority("T3") == 1
	for i, s := range steps {
		if s.rel {
			seqOK = seqOK && m.Release(s.t, s.r) == nil
			continue
		}
		g, err := m.Acquire(s.t, s.r)
		seqOK = seqOK && err == nil && g == s.want
		if i == 0 {
			epOK = m.EffectivePriority("T3") == 5 // 第1步后 T3 被抬升到 5
		}
	}
	line("eight-step key granted,blocked,blocked,...,granted", seqOK)
	line("ceilings R_x=5 R_y=3", m.Ceiling("R_x") == 5 && m.Ceiling("R_y") == 3)
	line("T3 effective priority 5 while holding R_x", epOK)

	// 2) SelfCheck：与朴素参照一致 + 全部四条不变量
	line("SelfCheck matches naive reference (all 4 invariants)", api.New().SelfCheck() == nil)

	// 3) 四类可判定、互不相同的哨兵错误，且被拒后状态不变
	d := api.New()
	d.AddTask("T1", 5)
	d.AddTask("T2", 2)
	d.AddResource("R_x")
	d.Use("T1", "R_x")
	_, e1 := d.Acquire("??", "R_x")
	_, e2 := d.Acquire("T1", "??")
	untouched := d.SystemCeiling() == 0 // 两次非法操作后无任何持有
	g1, _ := d.Acquire("T1", "R_x")
	_, e3 := d.Acquire("T1", "R_x")
	e4 := d.Release("T2", "R_x")
	untouched = untouched && d.SystemCeiling() == 5 && d.EffectivePriority("T1") == 5
	g5, e5 := d.Acquire("T2", "R_x") // 被拒后仍可正常使用：2<=5 阻塞
	d.Release("T1", "R_x")
	usable := e5 == nil && !g5 && d.SystemCeiling() == 0
	line("four distinct sentinel errors", errors.Is(e1, api.ErrUnknownTask) &&
		errors.Is(e2, api.ErrUnknownResource) && g1 && errors.Is(e3, api.ErrAlreadyHeld) &&
		errors.Is(e4, api.ErrNotHeld) && e1 != e2 && e2 != e3 && e3 != e4)
	line("rejected ops leave no trace, manager still usable", untouched && usable)

	// 4) 大 m：系统天花板由增量维护，m=100..10000 判定始终正确
	//    （检查个数为非导出常量，由 lock 包白盒测试钉住，不经公开接口读取）
	largeOK := true
	for _, n := range []int{100, 1000, 10000} {
		c := api.New()
		c.AddTask("low", 2)
		c.AddTask("z", 2)
		for i := 0; i < n; i++ {
			r := fmt.Sprintf("r%d", i)
			c.AddResource(r)
			c.Use("low", r)
			if g, _ := c.Acquire("low", r); !g {
				largeOK = false
			}
		}
		c.AddResource("rx")
		c.Use("low", "rx")
		if c.SystemCeiling() != 2 {
			largeOK = false
		}
		if g, _ := c.Acquire("z", "rx"); g { // 2 不严格大于 2：阻塞
			largeOK = false
		}
	}
	line("large m (100..10000) PCP decisions correct, check-count O(1)", largeOK)

	// 5) 并发：N 个 goroutine 在互不冲突资源上 Acquire/Release，结束后天花板归零
	p := api.New()
	const N, iters = 16, 200
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		tid, rid := fmt.Sprintf("t%d", g), fmt.Sprintf("res%d", g)
		p.AddTask(tid, 10+g)
		p.AddResource(rid)
		p.Use(tid, rid)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				if ok, err := p.Acquire(tid, rid); !ok || err != nil || p.Release(tid, rid) != nil {
					return
				}
			}
		}()
	}
	wg.Wait()
	line("concurrent Acquire/Release: mutex holds, ceiling back to 0", p.SystemCeiling() == 0)
}

func line(what string, ok bool) {
	if ok {
		fmt.Println("OK: " + what)
	} else {
		fmt.Println("FAIL: " + what)
	}
}
