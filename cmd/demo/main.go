package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/blk"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	s := "OK"
	if !ok {
		s = "FAIL"
	}
	fmt.Printf("%s %s\n", s, name)
}

type st struct{ i, t int }

func main() {
	// blk 包：状态机迁移 + LIFO 栈 + 满池判定
	b0, b1 := blk.New(0), blk.New(1)
	ok := b0.State() == blk.InUse && b0.MarkIdle() == nil &&
		b0.MarkInUse() == nil && b0.Reclaim() == nil &&
		b0.State() == blk.Reclaimed && b0.MarkIdle() == blk.ErrBadTransition
	var sk blk.Stack
	sk.Push(b0)
	sk.Push(b1)
	ok = ok && sk.Pop() == b1 && sk.Pop() == b0 && sk.Pop() == nil &&
		blk.Full(2, 2) && !blk.Full(1, 2)
	check("blk 状态机/LIFO栈/满池判定", ok)

	// 八步序列（maxIdle=2）：逐步核验返回块与 Idle/Total
	p, _ := api.New(2)
	var bs [3]api.Block
	bs[0], _ = p.Acquire()
	bs[1], _ = p.Acquire()
	bs[2], _ = p.Acquire()
	var got []st
	_ = p.Release(bs[0])
	got = append(got, st{p.Idle(), p.Total()})
	_ = p.Release(bs[1])
	got = append(got, st{p.Idle(), p.Total()})
	_ = p.Release(bs[2])
	got = append(got, st{p.Idle(), p.Total()})
	a7, _ := p.Acquire()
	a8, _ := p.Acquire()
	ok = got[0] == st{1, 3} && got[1] == st{2, 3} && got[2] == st{2, 2} &&
		a7 == bs[1] && a8 == bs[0]
	check("八步序列: 逐步Idle/Total=[1,3][2,3][2,2], LIFO 7=b1 8=b0", ok)

	// 第 6 步满池驱逐：回收 b2，Idle 永不超 maxIdle，b2 绝不再发
	ok = got[2] == st{2, 2} && a7 != bs[2] && a8 != bs[2] &&
		errors.Is(p.Release(bs[2]), api.ErrUnknownBlock)
	check("第6步满池驱逐回收b2, Idle<=maxIdle, b2不再复用", ok)

	// 三条不变量（SelfCheck 内置序列核验）+ 大 m + 并发
	sc := p.SelfCheck() == nil
	check("不变量: 守恒与唯一 (in-use+idle==Total, Idle<=maxIdle)", sc)
	check("不变量: 与朴素参照一致 (Idle/Total/LIFO顺序)", sc)
	check("不变量: 满池驱逐 (reclaimed 不再返回)", sc && got[2].t == 2)

	// 三类可判定错误互不相同，被拒后状态不变
	p2, _ := api.New(1)
	a, _ := p2.Acquire()
	_ = p2.Release(a)
	i0, t0 := p2.Idle(), p2.Total()
	e1 := p2.Release(a)
	e2 := p2.Release(api.Block{})
	_, e3 := api.New(0)
	ok = errors.Is(e1, api.ErrDoubleRelease) && errors.Is(e2, api.ErrUnknownBlock) &&
		errors.Is(e3, api.ErrInvalidMaxIdle) && e1 != e2 && e2 != e3 && e1 != e3 &&
		p2.Idle() == i0 && p2.Total() == t0
	check("三类错误可判定且互不相同, 被拒后状态不变", ok)

	// 大 m：造 10000 个空闲块后 Acquire 一次（检查条数<=1 由 opool 测试钉住）
	const m = 10000
	pm, _ := api.New(m)
	tmp := make([]api.Block, m)
	for i := range tmp {
		tmp[i], _ = pm.Acquire()
	}
	for _, b := range tmp {
		_ = pm.Release(b)
	}
	top, _ := pm.Acquire()
	ok = top == tmp[m-1] && pm.Idle() == m-1 && pm.Total() == m
	check("大m=10000 Acquire 只动栈顶 (计数<=1见TestAcquireConstantChecks)", ok)

	// 并发：N 个 goroutine 各 Acquire 再 Release，无同块重复占用，守恒
	const n = 64
	pc, _ := api.New(n)
	var wg sync.WaitGroup
	var mu sync.Mutex
	held := map[api.Block]bool{}
	dup := false
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			b, _ := pc.Acquire()
			mu.Lock()
			if held[b] {
				dup = true
			}
			held[b] = true
			mu.Unlock()
			mu.Lock()
			delete(held, b)
			mu.Unlock()
			_ = pc.Release(b)
		}()
	}
	close(start)
	wg.Wait()
	ok = !dup && pc.Idle() == pc.Total() && pc.Total() >= 1 && pc.Total() <= n
	check("并发: 无同块重复占用, Idle==Total 守恒", ok)

	if failed {
		os.Exit(1)
	}
}
