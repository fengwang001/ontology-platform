package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/vc"
)

var failed bool

func ok(cond bool, label string) {
	if !cond {
		failed = true
		fmt.Println("FAIL", label)
		return
	}
	fmt.Println("OK", label)
}

func vcs(s *api.System) (r [3][3]int) {
	for p := 0; p < 3; p++ {
		v, _ := s.VC(p)
		copy(r[p][:], v)
	}
	return
}

func tag(d bool) string {
	if d {
		return "投递"
	}
	return "阻塞"
}

func main() {
	// 第三节七步：每步之后的三个节点向量时钟与投递结果。
	s := api.New(3)
	var got [7][3][3]int
	var res [7]string
	m1, _ := s.Broadcast(0)
	got[0], res[0] = vcs(s), "广播M1"
	d, _ := s.Deliver(1, m1)
	got[1], res[1] = vcs(s), tag(d)
	m2, _ := s.Broadcast(1)
	got[2], res[2] = vcs(s), "广播M2"
	d, _ = s.Deliver(2, m1)
	got[3], res[3] = vcs(s), tag(d)
	d, _ = s.Deliver(2, m2)
	got[4], res[4] = vcs(s), tag(d)
	m3, _ := s.Broadcast(1)
	got[5], res[5] = vcs(s), "广播M3"
	d, _ = s.Deliver(0, m3)
	got[6], res[6] = vcs(s), tag(d)
	want := [7][3][3]int{
		{{1, 0, 0}, {0, 0, 0}, {0, 0, 0}}, {{1, 0, 0}, {1, 0, 0}, {0, 0, 0}},
		{{1, 0, 0}, {1, 1, 0}, {0, 0, 0}}, {{1, 0, 0}, {1, 1, 0}, {1, 0, 0}},
		{{1, 0, 0}, {1, 1, 0}, {1, 1, 0}}, {{1, 0, 0}, {1, 2, 0}, {1, 1, 0}},
		{{1, 0, 0}, {1, 2, 0}, {1, 1, 0}},
	}
	wantRes := [7]string{"广播M1", "投递", "广播M2", "投递", "投递", "广播M3", "阻塞"}
	for i := 0; i < 7; i++ {
		g := got[i]
		ok(g == want[i] && res[i] == wantRes[i],
			fmt.Sprintf("S%d VC0=%v VC1=%v VC2=%v %s", i+1, g[0], g[1], g[2], res[i]))
	}

	// 三类可判定错误互不相同，被拒后状态不变。
	f := api.New(2)
	fm, _ := f.Broadcast(0)
	fb0, _ := f.VC(0)
	fb1, _ := f.VC(1)
	_, e1 := f.Broadcast(9)
	_, e2 := f.Deliver(1, api.Msg{From: 0, TS: vc.Vector{7, 0}})
	_, e3 := f.Deliver(0, fm)
	fa0, _ := f.VC(0)
	fa1, _ := f.VC(1)
	distinct := errors.Is(e1, api.ErrNodeOutOfRange) && errors.Is(e2, api.ErrUnknownMessage) &&
		errors.Is(e3, api.ErrSelfDelivery) && e1 != e2 && e2 != e3 && e1 != e3
	intact := vc.Vector(fb0).Equal(fa0) && vc.Vector(fb1).Equal(fa1)
	ok(distinct && intact, "3 faults distinct, state intact")

	// 大 m 下 Deliver 阻塞行为一致；检查个数不随 m 增长由测试钉住。
	bigOK := true
	for _, m := range []int{100, 1000, 10000} {
		bs := api.New(2)
		msgs := make([]api.Msg, m)
		for i := range msgs {
			msgs[i], _ = bs.Broadcast(0)
		}
		d, err := bs.Deliver(1, msgs[m/2]) // 阻塞：前序消息未投递
		v1, _ := bs.VC(1)
		if err != nil || d || !vc.Vector(v1).Equal(vc.New(2)) {
			bigOK = false
		}
	}
	ok(bigOK, "big-m deliver blocked, O(1) count pinned by TestDeliverCheckedCountConstant")

	// 并发只读同一节点 VC，结果逐字段相同；并跑 SelfCheck。
	c := api.New(3)
	cm, _ := c.Broadcast(0)
	_, _ = c.Deliver(1, cm)
	base, _ := c.VC(0)
	const G = 32
	start := make(chan struct{})
	same := make(chan bool, G)
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			v, err := c.VC(0)
			same <- err == nil && vc.Vector(base).Equal(v)
		}()
	}
	close(start)
	wg.Wait()
	close(same)
	allSame := true
	for b := range same {
		allSame = allSame && b
	}
	ok(allSame && api.New(3).SelfCheck() == nil, "concurrent VC reads identical; SelfCheck")

	if failed {
		os.Exit(1)
	}
}
