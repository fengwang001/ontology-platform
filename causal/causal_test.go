package causal

import (
	"errors"
	"math/rand"
	"slices"
	"sync"
	"testing"

	"ontology/vc"
)

func flat(s *System) (a [9]int) { copy(a[:], slices.Concat(s.clocks...)); return }

// ck 是表驱动用的布尔断言助手，把 if-Fatal 样板收成一行调用。
func ck(t *testing.T, cond bool, f string, a ...any) {
	if !cond {
		t.Fatalf(f, a...)
	}
}

// TestSevenSteps 回放 NOTES 七步表；尾部核验(甲)缺 M1 阻塞、补齐放行，(乙)VC2=[1 1 0]。
func TestSevenSteps(t *testing.T) {
	s := New(3)
	var ms []vc.Message
	ops := []struct {
		fr, to, mi int
		ok         bool
		w          [9]int
	}{
		{0, -1, 0, true, [9]int{1, 0, 0, 0, 0, 0, 0, 0, 0}},
		{-1, 1, 0, true, [9]int{1, 0, 0, 1, 0, 0, 0, 0, 0}},
		{1, -1, 1, true, [9]int{1, 0, 0, 1, 1, 0, 0, 0, 0}},
		{-1, 2, 0, true, [9]int{1, 0, 0, 1, 1, 0, 1, 0, 0}},
		{-1, 2, 1, true, [9]int{1, 0, 0, 1, 1, 0, 1, 1, 0}},
		{1, -1, 2, true, [9]int{1, 0, 0, 1, 2, 0, 1, 1, 0}},
		{-1, 0, 2, false, [9]int{1, 0, 0, 1, 2, 0, 1, 1, 0}},
	}
	for i, o := range ops {
		if o.fr >= 0 {
			m, err := s.Broadcast(o.fr)
			ck(t, err == nil, "S%d: %v", i+1, err)
			ms = append(ms, m)
		} else {
			ok, err := s.Deliver(o.to, ms[o.mi])
			ck(t, err == nil && ok == o.ok, "S%d ok=%v err=%v", i+1, ok, err)
		}
		ck(t, flat(s) == o.w, "S%d clocks=%v", i+1, flat(s))
	}
	u := New(3) // (甲)(乙)
	a, _ := u.Broadcast(0)
	u.Deliver(1, a)
	b, _ := u.Broadcast(1)
	ok1, e1 := u.Deliver(2, b)
	ck(t, !ok1 && e1 == nil, "(甲) want block, ok=%v err=%v", ok1, e1)
	u.Deliver(2, a)
	ok2, e2 := u.Deliver(2, b)
	ck(t, ok2 && e2 == nil, "(甲) want deliver, ok=%v err=%v", ok2, e2)
	ck(t, u.clocks[2][0] == 1 && u.clocks[2][1] == 1 && u.clocks[2][2] == 0, "(乙) VC2=%v", u.clocks[2])
}

// TestFaults 三类哨兵错误互不相同、拒绝无痕、之后系统仍可用（不变量4）。
func TestFaults(t *testing.T) {
	s := New(3)
	m1, _ := s.Broadcast(0)
	before := flat(s)
	_, e1 := s.Deliver(7, m1)
	_, e2 := s.Deliver(0, vc.Message{From: 1, TS: vc.Clock{0, 5, 0}})
	_, e3 := s.Deliver(0, m1)
	ck(t, errors.Is(e1, ErrNodeOutOfRange) && errors.Is(e2, ErrUnknownMessage) && errors.Is(e3, ErrSelfDelivery), "%v %v %v", e1, e2, e3)
	ck(t, e1 != e2 && e2 != e3 && e1 != e3, "errors not distinct")
	_, err := s.Broadcast(-1)
	ck(t, errors.Is(err, ErrNodeOutOfRange), "Broadcast(-1)=%v", err)
	ck(t, flat(s) == before, "state changed after reject")
	ok, err := s.Deliver(1, m1)
	ck(t, ok && err == nil, "unusable after rejects: %v", err)
}

// TestLookupConstant 多档缓冲规模下阻塞投递检查数恒为 1，不随 m 线性增长。
func TestLookupConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := New(2)
		var last vc.Message
		for i := 0; i < m; i++ {
			last, _ = s.Broadcast(0)
		}
		ok, err := s.Deliver(1, last)
		ck(t, !ok && err == nil && s.lastCheck == 1, "m=%d checked=%d", m, s.lastCheck)
	}
}

// TestConcurrentVC：N 个 goroutine 并发只读同一节点时钟，逐字段一致；不使用 sleep。
func TestConcurrentVC(t *testing.T) {
	s := New(3)
	m, _ := s.Broadcast(0)
	_, e := s.Deliver(1, m)
	ck(t, e == nil, "%v", e)
	c := make(chan vc.Clock, 64)
	var wg sync.WaitGroup
	wg.Add(64)
	for i := 0; i < 64; i++ {
		go func() { defer wg.Done(); v, _ := s.VC(1); c <- v }()
	}
	wg.Wait()
	close(c)
	w := <-c
	for v := range c {
		ck(t, equalClock(v, w), "inconsistent %v vs %v", v, w)
	}
}

// TestInvariantsFuzz 随机操作/到达顺序：独立朴素重算的模型时钟对照规范纯函数
// vc.Deliverable 预测可投递性，逐操作比对（钉住不变量1/2/3）；模型仅在成功时推进。
func TestInvariantsFuzz(t *testing.T) {
	r := rand.New(rand.NewSource(732))
	for it := 0; it < 15; it++ {
		n := 2 + r.Intn(3) // n∈{2,3,4}，下面据此切片出 n×n 模型矩阵
		s := New(n)
		mc := [][]int{make([]int, n), make([]int, n), make([]int, n), make([]int, n)}[:n]
		done := map[[3]int]bool{}
		var msgs []vc.Message
		for st := 0; st < 40; st++ {
			if len(msgs) == 0 || r.Intn(3) == 0 {
				q := r.Intn(n)
				m, err := s.Broadcast(q)
				ck(t, err == nil, "%v", err)
				msgs, mc[q][q] = append(msgs, m), mc[q][q]+1
				continue
			}
			m := msgs[r.Intn(len(msgs))]
			p, q := r.Intn(n), m.From
			got, err := s.Deliver(p, m)
			id := [3]int{p, q, m.TS[q]}
			if p == q {
				ck(t, errors.Is(err, ErrSelfDelivery), "%v", err)
			} else if done[id] {
				ck(t, errors.Is(err, ErrUnknownMessage), "%v", err)
			} else {
				want := vc.Deliverable(vc.Clock(mc[p]), m.TS, q)
				ck(t, err == nil && got == want, "it%d %v got%v", it, m.TS, got)
				if got {
					done[id], mc[p][q] = true, mc[p][q]+1
				}
			}
		}
		for p := 0; p < n; p++ {
			ck(t, equalClock(s.clocks[p], vc.Clock(mc[p])), "it%d node%d", it, p)
		}
	}
}
