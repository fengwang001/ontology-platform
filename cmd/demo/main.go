// 演示程序：逐条打印 OK/FAIL，退出码 0 表示全部通过。不读参数、不联网。
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/cmt"
	"ontology/ofs"
)

var failed bool

func ok(name string, cond bool) {
	s := "OK  "
	if !cond {
		s, failed = "FAIL", true
	}
	fmt.Println(s, name)
}

func main() {
	c := api.New(16)
	c.Assign(0, 100)
	for off := int64(100); off <= 107; off++ {
		c.Deliver(0, off)
	}
	var seq []int64
	for _, off := range []int64{103, 100, 101, 105, 101, 102, 107} { // 第三节七步
		c.Ack(0, off)
		v, _ := c.Committed(0)
		seq = append(seq, v)
	}
	ok("七步 Ack 后 Committed="+fmt.Sprint(seq), fmt.Sprint(seq) == "[100 101 102 102 102 104 104]")

	c.Restart() // 重启后从 C=104 重新投递
	var redel []int64
	for off := int64(104); off <= 107; off++ {
		if c.Deliver(0, off) == nil {
			redel = append(redel, off)
		}
	}
	ok("重启后重复投递="+fmt.Sprint(redel)+"(105,107 为已处理的重复)", fmt.Sprint(redel) == "[104 105 106 107]")

	rng := rand.New(rand.NewSource(7)) // 随机 Ack 顺序对照朴素参照
	c2 := api.New(1 << 20)
	c2.Assign(0, 500)
	acked := map[int64]bool{}
	for i := 0; i < 64; i++ {
		c2.Deliver(0, 500+int64(i))
	}
	consistent := true
	for _, i := range rng.Perm(64) {
		c2.Ack(0, 500+int64(i))
		acked[500+int64(i)] = true
		want := int64(500)
		for acked[want] {
			want++
		}
		if got, _ := c2.Committed(0); got != want {
			consistent = false
		}
	}
	ok("随机 Ack 顺序与朴素参照一致", consistent)

	before, _ := c.Committed(0) // 重复 Ack 幂等：同一位点 Ack 两次、< C 的 Ack，均不报错不改状态
	dupOK := c.Ack(0, 105) == nil && c.Ack(0, 105) == nil && c.Ack(0, 100) == nil
	after, _ := c.Committed(0)
	ok("重复 Ack 幂等", dupOK && before == after)

	e1 := c.Ack(9, 0) // 四类可判定错误，互不相同
	e2 := c.Deliver(0, 999)
	e3 := c.Ack(0, 108)
	c3 := api.New(1)
	c3.Assign(0, 0)
	c3.Deliver(0, 0)
	e4 := c3.Deliver(0, 1)
	distinct := errors.Is(e1, cmt.ErrUnassigned) && errors.Is(e2, ofs.ErrGap) &&
		errors.Is(e3, ofs.ErrOutOfRange) && errors.Is(e4, cmt.ErrTooManyInFlight) &&
		e1 != e2 && e2 != e3 && e3 != e4 && e1 != e4
	ok("四类错误可判定且互不相同", distinct)

	snap := fmt.Sprint(c.Commit(), c3.Commit()) // 被拒后状态不变、仍可继续用
	c.Ack(9, 1)
	c.Deliver(0, 999)
	c3.Deliver(0, 1)
	stable := fmt.Sprint(c.Commit(), c3.Commit()) == snap
	v3, _ := c3.Committed(0)
	ok("被拒后状态不变且仍可用", stable && c3.Ack(0, 0) == nil && func() bool { w, _ := c3.Committed(0); return w == v3+1 }())

	largeOK := true // 大 m：推进只在 C 被 Ack 时发生（检查个数由 ofs 包内测试钉住）
	for _, m := range []int{100, 1000, 10000} {
		cm := api.New(m + 2)
		cm.Assign(0, 0)
		for i := int64(0); i <= int64(m); i++ {
			cm.Deliver(0, i)
		}
		for i := int64(1); i < int64(m); i++ {
			cm.Ack(0, i)
		}
		cm.Ack(0, int64(m)) // 不在 C 上：不得推进
		if v, _ := cm.Committed(0); v != 0 {
			largeOK = false
		}
		cm.Ack(0, 0) // Ack C：一次推进到位
		if v, _ := cm.Committed(0); v != int64(m)+1 {
			largeOK = false
		}
	}
	ok("大 m 推进正确且不整表重扫(m=100..10000)", largeOK)

	const n = 500 // 并发 Ack：最终 Committed=起点+N，期间读单调不减
	cc := api.New(n)
	cc.Assign(0, 0)
	for i := int64(0); i < n; i++ {
		cc.Deliver(0, i)
	}
	var doneCnt atomic.Int64
	var monoBad atomic.Bool
	start := make(chan struct{})
	go func() {
		prev := int64(0)
		for doneCnt.Load() < n {
			v, _ := cc.Committed(0)
			if v < prev {
				monoBad.Store(true)
			}
			prev = v
		}
	}()
	var wg sync.WaitGroup
	for i := int64(0); i < n; i++ {
		wg.Add(1)
		go func(off int64) { defer wg.Done(); <-start; cc.Ack(0, off); doneCnt.Add(1) }(i)
	}
	close(start)
	wg.Wait()
	vN, _ := cc.Committed(0)
	ok("并发 Ack 后 Committed=起点+N 且读单调", !monoBad.Load() && vN == n)

	ok("SelfCheck 四条不变量", api.New(8).SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
