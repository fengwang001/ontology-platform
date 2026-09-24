// Command demo 逐条打印位点提交器关键语义的 OK/FAIL，不读参数、不联网。
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sync"
	"sync/atomic"

	"ontology/api"
)

var failed bool

func chk(name string, ok bool) {
	if ok {
		fmt.Println("OK  ", name)
	} else {
		fmt.Println("FAIL", name)
		failed = true
	}
}

func naiveCommitted(s int64, a map[int64]bool) int64 {
	for a[s] {
		s++
	}
	return s
}

// deliverN 从 from 起连续投递 n 条。
func deliverN(c *api.Committer, p int, from, n int64) {
	for i := int64(0); i < n; i++ {
		_ = c.Deliver(p, from+i)
	}
}

func main() {
	c := api.New(0) // 第三节：七步 Committed(0)
	c.Assign(0, 100)
	deliverN(c, 0, 100, 8)
	acks := []int64{103, 100, 101, 105, 101, 102, 107}
	want := []int64{100, 101, 102, 102, 102, 104, 104}
	got := []int64{}
	for _, a := range acks {
		_ = c.Ack(0, a)
		v, _ := c.Committed(0)
		got = append(got, v)
	}
	chk(fmt.Sprintf("七步Committed=%v", got), reflect.DeepEqual(got, want))
	C, _ := c.Committed(0) // Restart 后重复投递 {104,105,106,107}
	c.Restart()
	good := true
	for o := C; o < 108; o++ {
		good = good && c.Deliver(0, o) == nil
	}
	v2, _ := c.Committed(0)
	chk("重启后重投[104 105 106 107]", good && v2 == 104)
	d := api.New(0) // 随机 Ack 顺序与朴素参照一致
	d.Assign(1, 0)
	am := map[int64]bool{}
	deliverN(d, 1, 0, 50)
	match := true
	for _, idx := range rand.New(rand.NewSource(7)).Perm(50) {
		o := int64(idx)
		_ = d.Ack(1, o)
		am[o] = true
		g, _ := d.Committed(1)
		match = match && g == naiveCommitted(0, am)
	}
	chk("随机Ack顺序=朴素参照", match)
	e := api.New(0) // 重复 Ack 幂等
	e.Assign(0, 0)
	_ = e.Deliver(0, 0)
	base, _ := e.Committed(0)
	_ = e.Ack(0, 0)
	a1, _ := e.Committed(0)
	_ = e.Ack(0, 0)
	a2, _ := e.Committed(0)
	chk("重复Ack幂等", a1 == base+1 && a2 == a1)
	f := api.New(1) // 四类可判定错误（互异由 TestRejectedLeavesNoTrace 钉）
	f.Assign(0, 10)
	errs := []error{f.Ack(9, 0), f.Deliver(0, 11)}
	_ = f.Deliver(0, 10)
	errs = append(errs, f.Deliver(0, 11), f.Ack(0, 99))
	wantE := []error{api.ErrPartitionNotAssigned, api.ErrDeliverGap, api.ErrTooManyInFlight, api.ErrAckOutOfRange}
	classified := true
	for i := range errs {
		if !errors.Is(errs[i], wantE[i]) {
			classified = false
		}
	}
	chk("四类哨兵错误可判定", classified)
	s0, _ := f.Committed(0) // 被拒后状态不变且仍可继续使用
	usable := f.Ack(0, 10) == nil && f.Deliver(0, 11) == nil
	s1, _ := f.Committed(0)
	chk("被拒后状态不变且仍可用", s0 == 10 && usable && s1 == 11)
	g := api.New(0) // 大 m：非 C 不推进、Ack(C) 一次越过全部
	const m = int64(10000)
	g.Assign(0, 0)
	deliverN(g, 0, 0, m+1)
	for o := int64(1); o < m; o++ {
		_ = g.Ack(0, o)
	}
	_ = g.Ack(0, m)
	gv, _ := g.Committed(0)
	_ = g.Ack(0, 0)
	ev, _ := g.Committed(0)
	chk("大m检查个数不随m增长(计数白盒断言); 非C不推进/Ack(C)全推进", gv == 0 && ev == m+1)
	h := api.New(0) // 并发 Ack 后 C 正确且单调不减
	h.Assign(0, 100)
	const N = 500
	deliverN(h, 0, 100, N)
	stop := make(chan struct{})
	var rwg, awg sync.WaitGroup
	var mono atomic.Bool
	rwg.Add(1)
	go func() {
		defer rwg.Done()
		prev := int64(-1)
		for {
			select {
			case <-stop:
				return
			default:
				cv, _ := h.Committed(0)
				if cv < prev {
					mono.Store(true)
				}
				prev = cv
			}
		}
	}()
	for _, idx := range rand.New(rand.NewSource(1)).Perm(N) {
		awg.Add(1)
		go func(o int64) { defer awg.Done(); _ = h.Ack(0, o) }(100 + int64(idx))
	}
	awg.Wait()
	close(stop)
	rwg.Wait()
	fin, _ := h.Committed(0)
	chk(fmt.Sprintf("并发Ack: C=%d 单调不减", fin), fin == 100+N && !mono.Load())
	chk("SelfCheck", api.New(0).SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
