// Command demo exercises the in-memory buffer pool and prints one OK/FAIL line per check.
package main

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/pool"
)

var failed bool

func fs(fs []pool.FrameSnapshot) string {
	out := make([]string, len(fs))
	for i, f := range fs {
		if f.PageID == -1 {
			out[i] = "-"
			continue
		}
		d := byte('c')
		if f.Dirty {
			d = 'd'
		}
		out[i] = fmt.Sprintf("(%d,%c,%d)", f.PageID, d, f.Pin)
	}
	return strings.Join(out, " ")
}
func check(name, detail string, ok bool) {
	tag := "OK"
	if !ok {
		tag, failed = "FAIL", true
	}
	fmt.Printf("%s %s %s\n", tag, name, detail)
}
func resident(fs []pool.FrameSnapshot) (int, bool) {
	seen := map[int]bool{}
	for _, f := range fs {
		if f.PageID == -1 {
			continue
		}
		if seen[f.PageID] {
			return len(seen), false
		}
		seen[f.PageID] = true
	}
	return len(seen), true
}

func main() {
	// 第三节：numFrames=3 的八步，逐步记录帧号/三帧状态/writes。
	p := pool.New(3)
	var b strings.Builder
	step := func(op string, fr int) { fmt.Fprintf(&b, "%s->f%d[%s]w%d ", op, fr, fs(p.Snapshot()), p.Writes()) }
	f1, _ := p.Pin(1)
	step("P1", f1)
	f2, _ := p.Pin(2)
	step("P2", f2)
	f3, _ := p.Pin(3)
	step("P3", f3)
	_ = p.MarkDirty(0)
	step("D0", 0)
	_ = p.Unpin(0)
	step("U0", 0)
	f4, _ := p.Pin(4)
	step("P4", f4)
	_ = p.Unpin(1)
	step("U1", 1)
	f5, _ := p.Pin(5)
	step("P5", f5)
	check("eight-steps", strings.TrimSpace(b.String()), f1 == 0 && f2 == 1 && f3 == 2 && f4 == 0 && f5 == 1)
	check("dirty-flush1/clean-flush0", fmt.Sprintf("writes=%d", p.Writes()), p.Writes() == 1)

	rf, _ := p.Pin(4)
	np, uniq := resident(p.Snapshot())
	check("invariant uniqueness", fmt.Sprintf("pages=%d rePin->f%d", np, rf), uniq && rf == f4)
	check("invariant naive-reference", "SelfCheck", api.New(3).SelfCheck() == nil)

	// 不变量3 pin 安全 + 三类可判定错误 + 被拒不留痕（1 帧池）。
	q := pool.New(1)
	q.Pin(9)
	before, bw := q.Snapshot(), q.Writes()
	_, eFull := q.Pin(10)
	fullNoTrace := reflect.DeepEqual(before, q.Snapshot()) && bw == q.Writes()
	pinnedGone := q.Snapshot()[0].PageID != 9
	q.Unpin(0)
	mid, mw := q.Snapshot(), q.Writes()
	eUnpin, eFrame := q.Unpin(0), q.Unpin(7)
	traceOK := reflect.DeepEqual(mid, q.Snapshot()) && mw == q.Writes()
	distinct := eFull != eUnpin && eUnpin != eFrame && eFull != eFrame
	check("invariant pin-safe", eFull.Error(), eFull == pool.ErrNoEvictableFrame && !pinnedGone)
	check("errors-distinct/no-trace", fmt.Sprintf("full=%v unpin=%v frame=%v", eFull, eUnpin, eFrame),
		distinct && eUnpin == pool.ErrUnpinNotPinned && eFrame == pool.ErrInvalidFrame && fullNoTrace && traceOK)

	check("resident-lookup O(1)", "m=100,1000,10000", pool.New(1).VerifyResidentLookup() == nil)

	// 并发：N goroutine 并发 Pin 不同页；只读读者只见驻留页数单调不减。
	const n = 50
	c := pool.New(n)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	var mono atomic.Int32
	mono.Store(1)
	go func() { // 只读读者，无 sleep
		prev := 0
		for {
			select {
			case <-stop:
				return
			default:
				cnt, _ := resident(c.Snapshot())
				if cnt < prev {
					mono.Store(0)
				}
				prev = cnt
				runtime.Gosched()
			}
		}
	}()
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(id int) {
			defer wg.Done()
			if _, e := c.Pin(id); e != nil {
				mono.Store(0)
			}
		}(i)
	}
	wg.Wait()
	close(stop)
	cn, cu := resident(c.Snapshot())
	check("concurrency", fmt.Sprintf("resident=%d/%d unique=%v monotonic=%d writes=%d", cn, n, cu, mono.Load(), c.Writes()),
		cn == n && cu && mono.Load() == 1 && c.Writes() == 0)

	if failed {
		os.Exit(1)
	}
}
