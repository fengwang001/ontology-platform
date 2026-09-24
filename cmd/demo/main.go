// Command demo 是本题的可运行演示：不读参数、不联网，全部判定 OK 时退出码 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/api"
)

func rng(a *api.API) string {
	if es := a.Read(0); len(es) > 0 {
		return fmt.Sprintf("[%d,%d]", es[0].Offset, es[len(es)-1].Offset)
	}
	return "empty"
}

func state(a *api.API) string {
	return fmt.Sprintf("%d/%d/%d/%v", a.CP(), a.Marker(), a.First(), a.Read(0))
}

var fails int
var results []string

func chk(b bool, name string) {
	tag := "OK"
	if !b {
		tag, fails = "FAIL", fails+1
	}
	results = append(results, name+"="+tag)
}

func refEquiv() bool { // 与朴素参照一致
	c := api.New()
	ref := map[uint64]string{}
	match := func() bool {
		es := c.Read(0)
		if uint64(len(es)) != uint64(len(ref))-c.First() {
			return false
		}
		for i, e := range es {
			if e.Offset != c.First()+uint64(i) || ref[e.Offset] != e.Payload {
				return false
			}
		}
		return true
	}
	for _, p := range []string{"a", "b", "c", "d"} {
		off, _ := c.Append(p)
		ref[off] = p
		if !match() {
			return false
		}
	}
	c.Checkpoint(2)
	c.Truncate(2)
	c.Checkpoint(3)
	return c.Recover(3, 2) == nil && c.Marker() == 3 && c.First() == 3 && match()
}

func largeM() bool { // 大 m 截断正确；cp 读取个数 O(1) 由 TestCPReadCount 钉死，计数器不导出
	c := api.New()
	const m = 10000
	for i := 0; i <= m; i++ {
		c.Append(fmt.Sprintf("p%d", i))
	}
	c.Checkpoint(m)
	if c.Truncate(m) != nil || c.First() != m {
		return false
	}
	es := c.Read(0)
	return len(es) == 1 && es[0].Offset == m
}

func concurrent() bool { // 并发读只见连贯区间
	c := api.New()
	var bad int32
	var wg sync.WaitGroup
	wg.Add(4)
	for r := 0; r < 3; r++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 2000; j++ {
				for k, es := 1, c.Read(0); k < len(es); k++ {
					if es[k].Offset != es[k-1].Offset+1 || es[k].Payload != fmt.Sprintf("p%d", es[k].Offset) {
						atomic.StoreInt32(&bad, 1)
					}
				}
			}
		}()
	}
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			c.Append(fmt.Sprintf("p%d", i))
			if i%8 == 7 {
				k := uint64(i - 1)
				c.Checkpoint(k)
				c.Truncate(k)
			}
		}
	}()
	wg.Wait()
	return bad == 0
}
func main() {
	a := api.New()
	step := func(n int, op string) {
		fmt.Printf("step%d %-7s cp=%-2d tm=%d f=%d read=%s\n", n, op, a.CP(), a.Marker(), a.First(), rng(a))
	}
	run := func(n int, op string, do func()) { do(); step(n, op) }
	run(1, "A(a)=0", func() { a.Append("a") })
	run(2, "A(b)=1", func() { a.Append("b") })
	run(3, "A(c)=2", func() { a.Append("c") })
	run(4, "C(2)", func() { a.Checkpoint(2) })
	run(5, "T(2)", func() { a.Truncate(2) })
	run(6, "A(d)=3", func() { a.Append("d") })
	run(7, "C(3)", func() { a.Checkpoint(3) })
	fmt.Printf("step8 T(3)+CR cp=%d tm=3 f=2 read=%s (marker durable, delete pending)\n", a.CP(), rng(a))
	chk(a.Recover(3, 2) == nil && a.Marker() == 3 && a.First() == 3, "converge f==tm")
	c := api.New() // K<=cp、三类互异哨兵、被拒不留痕、之后仍可用
	s0 := state(c)
	ok := errors.Is(c.Checkpoint(0), api.ErrCheckpointInvalid) && state(c) == s0
	c.Append("x")
	c.Append("y")
	c.Append("z")
	s1 := state(c)
	ok = ok && errors.Is(c.Truncate(3), api.ErrTruncateBeyondCP) && state(c) == s1
	c.Checkpoint(2)
	s2 := state(c)
	ok = ok && errors.Is(c.Checkpoint(1), api.ErrCheckpointInvalid) && state(c) == s2
	ok = ok && errors.Is(c.Checkpoint(3), api.ErrCheckpointInvalid) && state(c) == s2
	ok = ok && errors.Is(c.Recover(2, 3), api.ErrRecoverOverDeleted) && state(c) == s2
	ok = ok && c.Truncate(2) == nil && c.First() == 2
	ok = ok && api.ErrCheckpointInvalid != api.ErrTruncateBeyondCP &&
		api.ErrCheckpointInvalid != api.ErrRecoverOverDeleted && api.ErrTruncateBeyondCP != api.ErrRecoverOverDeleted
	chk(ok, "K<=cp|3-distinct-errors|no-trace")
	chk(refEquiv(), "naive-ref")
	chk(largeM(), "large-m(O(1)-cp-reads:TestCPReadCount)")
	chk(concurrent(), "concurrent-coherent")
	chk(a.SelfCheck() == nil, "SelfCheck")
	fmt.Println(strings.Join(results, " "))
	if fails > 0 {
		os.Exit(1)
	}
}
