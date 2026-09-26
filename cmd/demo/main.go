// demo 逐项核验 WFQ 的正确性，全部 OK 时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/wfq"
)

var fails atomic.Int32

func report(name, detail string, ok bool) {
	tag := "OK  "
	if !ok {
		tag = "FAIL"
		fails.Add(1)
	}
	fmt.Printf("%s %s %s\n", tag, name, detail)
}

// checkTrace 复现 NOTES.md 的八步推导（weights=[2,1]）。
func checkTrace() {
	s, _ := wfq.New([]int{2, 1})
	type step struct{ f0, f1, v, r int }
	want := []step{{1, 0, 0, -1}, {1, 1, 0, -1}, {1, 1, 1, 0}, {1, 1, 1, 1},
		{1, 3, 1, -1}, {1, 3, 3, 1}, {4, 3, 3, -1}, {4, 3, 4, 0}}
	ops := [][2]int{{0, 2}, {1, 1}, {-1, 0}, {-1, 0}, {1, 2}, {-1, 0}, {0, 2}, {-1, 0}}
	ok, d := true, ""
	for i, o := range ops {
		r := -1
		if o[0] < 0 {
			r, _ = s.Dequeue()
		} else {
			s.Submit(o[0], o[1])
		}
		g := step{s.Finish(0), s.Finish(1), s.VirtualTime(), r}
		ok = ok && g == want[i]
		d += fmt.Sprintf(" s%d:(%d,%d)V%d", i+1, g.f0, g.f1, g.v)
		if r >= 0 {
			d += fmt.Sprintf("=%d", r)
		}
	}
	report("trace w=[2,1]", d, ok)
}

// checkSelfCheck 逐条打印 SelfCheck 对四条不变量（五项）的内置核验。
func checkSelfCheck() {
	w, _ := api.New([]int{1})
	names := []string{"naive-consistent", "monotonic", "fairness", "distinct-errors", "reject-atomic"}
	for i, err := range w.SelfCheck() {
		report(names[i], "selfcheck", err == nil)
	}
}

func checkBigM() {
	const m = 10000
	ws := make([]int, m)
	for i := range ws {
		ws[i] = 1
	}
	s, _ := wfq.New(ws)
	for i := 0; i < m; i++ {
		s.Submit(i, i+1) // 权重全 1，完成时刻 1..m 互异
	}
	g, err := s.Dequeue()
	report("heap-select", "m=10000 distinct heads (count pinned by TestHeapCheckBound)",
		err == nil && g == 0 && s.VirtualTime() == 1)
}

func checkConcurrent() {
	w, _ := api.New([]int{2})
	const n = 64
	var wg sync.WaitGroup
	var hi atomic.Int64 // 任一时刻读到的 V 不得小于已见最大值
	var bad atomic.Bool
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.Submit(0, 2)
			v := int64(w.VirtualTime())
			for {
				old := hi.Load()
				if v < old {
					bad.Store(true)
					return
				}
				if hi.CompareAndSwap(old, v) {
					return
				}
			}
		}()
	}
	wg.Wait()
	prev, ok := 0, !bad.Load()
	for i := 0; i < n; i++ { // 队列恰为 N 且完成时刻升序（V 不减即升序）
		if _, err := w.Dequeue(); err != nil || w.VirtualTime() < prev {
			ok = false
		}
		prev = w.VirtualTime()
	}
	_, err := w.Dequeue()
	report("concurrent-submit", "64 goroutines -> 64 ascending packets, V monotonic",
		ok && errors.Is(err, api.ErrEmpty))
}

func main() {
	checkTrace()
	checkSelfCheck()
	checkBigM()
	checkConcurrent()
	if fails.Load() > 0 {
		os.Exit(1)
	}
}
