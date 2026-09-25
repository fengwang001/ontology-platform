// Command demo 对对象池实现做内置判定，全部 OK 时退出码为 0；不读参数不联网。
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync"

	"ontology/api"
)

func main() {
	fails := 0
	ok := func(name string, cond bool) {
		if cond {
			fmt.Println("OK: " + name)
		} else {
			fmt.Println("FAIL: " + name)
			fails++
		}
	}

	// 第三节：maxIdle=2 的八步序列（i=Idle,t=Total，[] 为 Release 后空闲栈）。
	p, _ := api.New(2)
	acq := func() api.Block { b, _ := p.Acquire(); return b }
	b0, b1, b2 := acq(), acq(), acq()
	trace := fmt.Sprintf("1:%s(i0,t1) 2:%s(i0,t2) 3:%s(i0,t3)", b0, b1, b2)
	c := b0.String() == "b0" && b1.String() == "b1" && b2.String() == "b2" &&
		p.Idle() == 0 && p.Total() == 3
	_ = p.Release(b0)
	trace += fmt.Sprintf(" 4:R%s[%s](i1,t3)", b0, b0)
	c = c && p.Idle() == 1 && p.Total() == 3
	_ = p.Release(b1)
	trace += fmt.Sprintf(" 5:R%s[%s,%s](i2,t3)", b1, b0, b1)
	c = c && p.Idle() == 2 && p.Total() == 3
	_ = p.Release(b2)
	trace += fmt.Sprintf(" 6:R%s->reclaim[%s,%s](i2,t2)", b2, b0, b1)
	c = c && p.Idle() == 2 && p.Total() == 2
	a1, a2 := acq(), acq()
	trace += fmt.Sprintf(" 7:%s(i1,t2) 8:%s(i0,t2)", a1, a2)
	c = c && p.Idle() == 0 && p.Total() == 2
	ok("eight steps: "+trace, c)
	ok("step6 full-pool eviction reclaims b2", b2.String() == "b2" && p.Total() == 2)
	ok("LIFO reuse: step7=b1 step8=b0", a1 == b1 && a2 == b0)

	// 随机序列对照朴素 LIFO 模型：上限、守恒、唯一、驱逐。
	q, _ := api.New(4)
	rng := rand.New(rand.NewSource(635))
	held, dead := map[api.Block]bool{}, map[api.Block]bool{}
	var heldList, idle []api.Block
	neverOver, conserve, unique, noDead := true, true, true, true
	for step := 0; step < 1000; step++ {
		if rng.Intn(2) == 0 || len(heldList) == 0 {
			g, _ := q.Acquire()
			noDead = noDead && !dead[g]
			unique = unique && !held[g]
			held[g] = true
			heldList = append(heldList, g)
			if len(idle) > 0 {
				n := len(idle)
				unique = unique && g == idle[n-1] // 朴素 LIFO 栈顶
				idle = idle[:n-1]
			}
		} else {
			i := rng.Intn(len(heldList))
			b := heldList[i]
			heldList = append(heldList[:i], heldList[i+1:]...)
			delete(held, b)
			_ = q.Release(b)
			if len(idle) >= 4 {
				dead[b] = true
			} else {
				idle = append(idle, b)
			}
		}
		neverOver = neverOver && q.Idle() <= 4
		conserve = conserve && q.Total() == len(held)+q.Idle()
	}
	ok("Idle never exceeds maxIdle under random ops", neverOver)
	ok("invariant conservation & unique ownership", conserve && unique)
	ok("invariant naive-reference & eviction (SelfCheck)", noDead && p.SelfCheck() == nil)

	// 三类互不相同的哨兵错误；被拒后状态不变。
	_, eNew := api.New(0)
	r, _ := api.New(1)
	x, _ := r.Acquire()
	_ = r.Release(x)
	eDup := r.Release(x)
	eUnk := r.Release(b2)
	ok("three distinct sentinel errors", errors.Is(eNew, api.ErrInvalidMaxIdle) &&
		errors.Is(eDup, api.ErrDuplicateRelease) && errors.Is(eUnk, api.ErrUnknownBlock) &&
		eNew != eDup && eDup != eUnk)
	ok("rejected ops leave no trace, pool still usable", r.Idle() == 1 && r.Total() == 1)

	// 大 m 下只动栈顶（检查条数的常数上界由 opool 同包测试直接读非导出计数器钉住）。
	topOnly := true
	for _, m := range []int{100, 1000, 10000} {
		s, _ := api.New(m)
		bs := make([]api.Block, m)
		for i := range bs {
			bs[i], _ = s.Acquire()
		}
		for _, b := range bs {
			_ = s.Release(b)
		}
		g, _ := s.Acquire()
		topOnly = topOnly && g == bs[m-1] && s.Idle() == m-1 && s.Total() == m
	}
	ok("O(1) top-only reuse for m=100/1000/10000", topOnly)

	// 并发：屏障分两阶段。阶段1：N 人各 Acquire 并持块等待（此阶段无释放，
	// 故必分配 N 个互异块，任何重复即同时双占）；阶段2：屏障开后统一 Release。
	// maxIdle=N：无驱逐，结束 Idle=Total=N 是确定稳定值，Total 守恒。
	const N = 200
	cp, _ := api.New(N)
	var wg sync.WaitGroup
	var hmu sync.Mutex
	holding := map[api.Block]bool{}
	var allAcq sync.WaitGroup
	allAcq.Add(N)
	badOwners := 0
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g, _ := cp.Acquire()
			hmu.Lock()
			if holding[g] {
				badOwners++
			}
			holding[g] = true
			hmu.Unlock()
			allAcq.Done()
			allAcq.Wait() // 等所有人都持块后再释放
			_ = cp.Release(g)
		}()
	}
	wg.Wait()
	ok("concurrency: unique ownership, stable Idle=Total=N", badOwners == 0 &&
		cp.Idle() == N && cp.Total() == N)

	if fails != 0 {
		os.Exit(1)
	}
}
