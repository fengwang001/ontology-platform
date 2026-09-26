package main

import (
	"errors"
	"fmt"
	"sync"

	"ontology/acpt"
	"ontology/api"
	"ontology/quorum"
)

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
	}
}

func main() {
	// acpt：同轮 accept（n==promised，按 >= 接受）与过期拒绝不留痕。
	var a acpt.Acceptor
	ok, ap, av := a.Prepare(2)
	acptOK := ok && ap == 0 && av == 0 && a.Accept(2, 10) &&
		a.Accepted() == 2 && a.AcceptedValue() == 10
	_, _, _ = a.Prepare(2)
	acptOK = acptOK && a.Promise() == 2 && !a.Accept(1, 99) &&
		a.Accepted() == 2 && a.AcceptedValue() == 10
	check("acpt: n==promised 接受；过期 Prepare/Accept 拒绝不留痕", acptOK)

	// quorum：第三节十步，逐次比对 Chosen()。
	c := quorum.New(3)
	steps := []func() bool{
		func() bool { ok, _ := c.Prepare(0, 2); return ok },
		func() bool { ok, _ := c.Prepare(1, 2); return ok },
		func() bool { ok, _ := c.Prepare(2, 2); return ok },
		func() bool { return c.Accept(0, 2, 10) },
		func() bool { return c.Accept(1, 2, 10) },
		func() bool { ok, _ := c.Prepare(0, 3); return ok },
		func() bool { ok, _ := c.Prepare(1, 3); return ok },
		func() bool { ok, _ := c.Prepare(2, 3); return ok },
		func() bool { return c.Accept(0, 3, 10) },
		func() bool { return c.Accept(1, 3, 10) },
	}
	wantV := []int{0, 0, 0, 0, 10, 10, 10, 10, 10, 10}
	gotV := make([]int, 10)
	tenOK := true
	for i, step := range steps {
		if !step() {
			tenOK = false
		}
		v, chosen := c.Chosen()
		if !chosen {
			v = 0
		}
		gotV[i] = v
		tenOK = tenOK && v == wantV[i]
	}
	check(fmt.Sprintf("quorum: 十步 Chosen=%v 期望=%v", gotV, wantV), tenOK)

	// 第 3 轮多数派回报里已接受号最大的值是 10，必须复用而非自用 20。
	v, reused := quorum.PickValue([]quorum.Report{
		{AcceptedProposal: 2, AcceptedValue: 10},
		{AcceptedProposal: 2, AcceptedValue: 10},
		{AcceptedProposal: 0, AcceptedValue: 0},
	}, 20)
	check("quorum: 第3轮复用已接受号最大的值 10", v == 10 && reused)

	// Chosen 只读增量计数表：各规模下内部 acceptor 读取数恒为 0。
	check("quorum: 大 m 下 Chosen 读取数不随规模增长",
		quorum.VerifyChosenReadBound([]int{101, 999, 4999, 9999}, 0))

	// api：自检四条不变量。
	p := api.New(3)
	check("api: SelfCheck 四条不变量成立", p.SelfCheck())

	// api：三类可判定错误互不相同，且拒绝后状态不变、实例仍可用。
	p2 := api.New(3)
	_, _, e1 := p2.Prepare(5, 1) // 下标越界
	_, _, e2 := p2.Prepare(0, 0) // 提案号非正
	_, _, _ = p2.Prepare(0, 1)
	_, _, e3 := p2.Prepare(0, 1) // 过期 prepare
	errOK := errors.Is(e1, api.ErrInvalidAcceptor) &&
		errors.Is(e2, api.ErrInvalidProposal) && errors.Is(e3, api.ErrStalePrepare)
	okAcc, _ := p2.Accept(0, 1, 7) // 此前的非法调用未污染 acceptor 0
	okAcc1, _ := p2.Accept(1, 1, 7)
	v, chosen := p2.Chosen()
	check("api: 三类哨兵错误互异；被拒后仍可正常使用", errOK && okAcc && okAcc1 && v == 7 && chosen)

	// api：N 个 goroutine 并发只读 Chosen，结果必须逐字段相同。
	ready := api.New(3)
	_, _, _ = ready.Prepare(0, 1)
	_, _, _ = ready.Prepare(1, 1)
	_, _ = ready.Accept(0, 1, 55)
	_, _ = ready.Accept(1, 1, 55)
	const N = 64
	res := make(chan [2]int, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cv, cok := ready.Chosen()
			b := 0
			if cok {
				b = 1
			}
			res <- [2]int{cv, b}
		}()
	}
	wg.Wait()
	close(res)
	first, concOK := [2]int{}, true
	for r := range res {
		if first == [2]int{0, 0} && !(r[0] == 0 && r[1] == 0) {
			first = r
		}
		if first != [2]int{0, 0} && r != first {
			concOK = false
		}
	}
	check("api: 64 goroutine 并发只读 Chosen 逐字段一致", concOK && first == [2]int{55, 1})
}
