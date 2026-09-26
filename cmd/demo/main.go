// Command demo 以不联网、不读参数的方式演示因果广播判定，逐条打印 OK/FAIL。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/causal"
	"ontology/vc"
)

var failed bool

func line(ok bool, format string, args ...any) {
	tag := "OK"
	if !ok {
		tag, failed = "FAIL", true
	}
	fmt.Printf("%s %s\n", tag, fmt.Sprintf(format, args...))
}

func vcs(a *api.API) [3]string {
	var out [3]string
	for p := range out {
		c, _ := a.VC(p)
		out[p] = fmt.Sprint([]int(c))
	}
	return out
}

func main() {
	a := api.New(3)
	var m1, m2, m3 vc.Message
	// 七步回放：每行给出期望的三个节点时钟与结果（bc=广播，del=投递，blk=阻塞）。
	rows := []struct {
		name string
		want [3]string
		res  string
		run  func() (string, error)
	}{
		{"S1", [3]string{"[1 0 0]", "[0 0 0]", "[0 0 0]"}, "bc", func() (string, error) { var e error; m1, e = a.Broadcast(0); return "bc", e }},
		{"S2", [3]string{"[1 0 0]", "[1 0 0]", "[0 0 0]"}, "del", func() (string, error) { ok, e := a.Deliver(1, m1); return r(ok), e }},
		{"S3", [3]string{"[1 0 0]", "[1 1 0]", "[0 0 0]"}, "bc", func() (string, error) { var e error; m2, e = a.Broadcast(1); return "bc", e }},
		{"S4", [3]string{"[1 0 0]", "[1 1 0]", "[1 0 0]"}, "del", func() (string, error) { ok, e := a.Deliver(2, m1); return r(ok), e }},
		{"S5", [3]string{"[1 0 0]", "[1 1 0]", "[1 1 0]"}, "del", func() (string, error) { ok, e := a.Deliver(2, m2); return r(ok), e }},
		{"S6", [3]string{"[1 0 0]", "[1 2 0]", "[1 1 0]"}, "bc", func() (string, error) { var e error; m3, e = a.Broadcast(1); return "bc", e }},
		{"S7", [3]string{"[1 0 0]", "[1 2 0]", "[1 1 0]"}, "blk", func() (string, error) { ok, e := a.Deliver(0, m3); return r(ok), e }},
	}
	for _, row := range rows {
		res, err := row.run()
		got := vcs(a)
		ok := err == nil && res == row.res && got == row.want
		line(ok, "%s %s VC0=%s VC1=%s VC2=%s", row.name, res, got[0], got[1], got[2])
	}

	// 三类可判定、互不相同的错误；拒绝后全节点时钟不变。
	before := vcs(a)
	_, eRange := a.Deliver(7, m1)
	_, eUnk := a.Deliver(0, vc.Message{From: 1, TS: vc.Clock{0, 5, 0}})
	_, eSelf := a.Deliver(0, m1)
	distinct := errors.Is(eRange, causal.ErrNodeOutOfRange) && errors.Is(eUnk, causal.ErrUnknownMessage) &&
		errors.Is(eSelf, causal.ErrSelfDelivery) && eRange != eUnk && eUnk != eSelf && eRange != eSelf
	line(distinct && vcs(a) == before, "faults range/unknown/self distinct, state unchanged after reject")

	// O(1) 查找：SelfCheck 内部以多档 m(100/1000/10000) 白盒核验 lastCheck 恒为 1（不暴露数值）。
	if scErr := api.New(3).SelfCheck(); scErr != nil {
		line(false, "invariants + O(1) lookup: %v", scErr)
	} else {
		line(true, "invariants + O(1) lookup (m=100/1000/10000, checked==1)")
	}

	// 并发只读同一节点时钟：N 个 goroutine 读到的向量逐字段相同。
	c := api.New(2)
	m, _ := c.Broadcast(0)
	c.Deliver(1, m) // VC[1] = [1 0]
	const N = 64
	var wg sync.WaitGroup
	res := make(chan string, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); v, _ := c.VC(1); res <- fmt.Sprint([]int(v)) }()
	}
	wg.Wait()
	close(res)
	first, same := "", true
	for s := range res {
		if first == "" {
			first = s
		} else if s != first {
			same = false
		}
	}
	line(same, "concurrent VC reads identical across %d goroutines (%s)", N, first)

	if failed {
		os.Exit(1)
	}
}

func r(delivered bool) string {
	if delivered {
		return "del"
	}
	return "blk"
}
