package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"

	"ontology/api"
)

type memSink struct {
	got     []api.Entry
	maxSize int
	calls   int
	failOn  int
}

func (s *memSink) Apply(b []api.Entry) error {
	s.maxSize = max(s.maxSize, len(b))
	s.calls++
	if s.failOn > 0 && s.calls == s.failOn {
		return errors.New("boom")
	}
	s.got = append(s.got, b...)
	return nil
}

var failed bool

func ok(name string, cond bool) {
	tag := "OK  "
	if !cond {
		tag, failed = "FAIL ", true
	}
	fmt.Println(tag + name)
}

// match 判定交付序列的 key 是否恰为 want（顺序、数量都一致）。
func match(es []api.Entry, want ...string) bool {
	if len(es) != len(want) {
		return false
	}
	for i := range want {
		if es[i].Key != want[i] {
			return false
		}
	}
	return true
}

func main() {
	// 十二步：T3 延迟 [a]，T7 批量 [b,c,d]，T12 len==H 拒收 i 且 [e..h] 不变
	s := &memSink{}
	b, _ := api.New(3, 4, 2, s)
	_ = b.Write("a", 1)
	_, _ = b.Tick()
	_, _ = b.Tick()
	for _, k := range []string{"b", "c", "d"} {
		_ = b.Write(k, 0)
	}
	_, _ = b.Tick()
	for _, k := range []string{"e", "f", "g", "h"} {
		_ = b.Write(k, 0)
	}
	before := b.Buffered()
	errI := b.Write("i", 9)
	_ = b.FlushAll()
	ok("十二步 T3延迟[a] T7批量[b,c,d] T12拒收且[e..h]不变",
		errors.Is(errI, api.ErrHighWater) && before == 4 &&
			match(s.got, "a", "b", "c", "d", "e", "f", "g", "h"))
	ok("每批条目数<=B(=3)", s.maxSize <= 3)
	// 第 2 批失败：整批回滚缓冲恢复[d,e,f]、Sink 仅[a,b,c]，重试不重
	r2 := &memSink{failOn: 2}
	r, _ := api.New(3, 4, 2, r2)
	for _, k := range []string{"a", "b", "c"} {
		_ = r.Write(k, 0)
	}
	_, _ = r.Tick()
	for _, k := range []string{"d", "e", "f"} {
		_ = r.Write(k, 0)
	}
	_, errT := r.Tick()
	rolled := errors.Is(errT, api.ErrSink) && r.Buffered() == 3 && r.Delivered() == 3 &&
		match(r2.got, "a", "b", "c")
	r2.failOn = 0
	_ = r.FlushAll()
	ok("第2批失败整体回滚[d,e,f] Sink仅[a,b,c] 重试不丢不重",
		rolled && match(r2.got, "a", "b", "c", "d", "e", "f"))
	// 四类哨兵错误互异可判定；高水位/空 key 拒收后状态不变
	g, _ := api.New(1, 1, 1, &memSink{})
	_ = g.Write("k", 1)
	_, eParam := api.New(0, 1, 1, &memSink{})
	eHigh := g.Write("j", 1)
	eEmpty := g.Write("", 1)
	ok("四类错误可判定互异 拒收后缓冲/交付不变",
		errors.Is(eParam, api.ErrInvalidParam) && errors.Is(eHigh, api.ErrHighWater) &&
			errors.Is(eEmpty, api.ErrEmptyKey) && errors.Is(errT, api.ErrSink) &&
			g.Buffered() == 1 && g.Delivered() == 0)

	// 大 m：一次强制 flush 恒只取头部 B 条（O(B) 访问由白盒测试钉住）
	headOK := true
	for _, m := range []int{100, 1000, 10000} {
		h, _ := api.New(3, m+1, 1<<30, &memSink{})
		for i := 0; i < m; i++ {
			_ = h.Write(strconv.Itoa(i), i)
		}
		n, _ := h.Flush()
		headOK = headOK && n == 3 && h.Buffered() == m-3 && h.Delivered() == 3
	}
	ok("大m(100/1k/10k)一次Flush仅取头部B条", headOK)

	// 并发：N goroutine 各写不同 key，并发读 Buffered 在界内，FlushAll 不丢不重
	const N = 200
	p, _ := api.New(7, N, 1<<30, &memSink{})
	stop := make(chan struct{})
	var bad int32
	var mon, wr sync.WaitGroup
	mon.Add(1)
	go func() {
		defer mon.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if v := p.Buffered(); v < 0 || v > N {
					atomic.StoreInt32(&bad, 1)
				}
			}
		}
	}()
	for i := 0; i < N; i++ {
		wr.Add(1)
		go func(i int) { defer wr.Done(); _ = p.Write("k"+strconv.Itoa(i), i) }(i)
	}
	wr.Wait()
	close(stop)
	mon.Wait()
	_ = p.FlushAll()
	ok("并发Write N=200 读值[0,H] FlushAll后恰N条不丢不重",
		atomic.LoadInt32(&bad) == 0 && p.Buffered() == 0 && p.Delivered() == N)
	ok("SelfCheck 四条不变量内置核验", b.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
