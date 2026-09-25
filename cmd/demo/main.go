package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/ckp"
	"ontology/rec"
)

var failed bool

func check(name string, ok bool) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

// checkCkp exercises ckp's advancement rules directly.
func checkCkp() {
	var st ckp.State
	ok := st.Apply(1, 5) == nil && st.Apply(2, 3) == nil
	st.Flush()
	st.Checkpoint()
	c, err := st.Restart()
	ok = ok && err == nil && c == (ckp.Ckpt{Pos: 2, Total: 8, Valid: true}) &&
		st.Total() == 8 && st.Applied() == 2 && st.Flushed() == 2
	ok = ok && st.Apply(2, 9) == ckp.ErrGap && st.Apply(0, 1) == ckp.ErrNonPositive &&
		st.Applied() == 2 && st.Total() == 8 // rejects left no trace
	check("ckp 规则: Apply/Flush/Checkpoint/Restart 推进与拒绝", ok)
}

// checkEightSteps verifies each of the 8 steps' four-tuple, then replays.
func checkEightSteps() {
	p := rec.New()
	type want struct {
		applied, flushed int64
		ckpt             rec.Ckpt
		total            int64
	}
	none, c28 := rec.Ckpt{}, rec.Ckpt{Pos: 2, Total: 8, Valid: true}
	steps := []struct {
		op   func()
		want want
	}{
		{func() { _ = p.Apply(1, 5) }, want{1, 0, none, 5}},
		{func() { _ = p.Apply(2, 3) }, want{2, 0, none, 8}},
		{p.Flush, want{2, 2, none, 8}},
		{func() { _ = p.Apply(3, -1) }, want{3, 2, none, 7}},
		{p.Checkpoint, want{3, 2, c28, 7}}, // step 5: ckpt=(2,8)
		{func() { _ = p.Apply(4, 6) }, want{4, 2, c28, 13}},
		{func() { _ = p.Apply(5, 2) }, want{5, 2, c28, 15}},
		{func() { _, _ = p.Restart() }, want{2, 2, c28, 8}}, // step 8
	}
	ok := true
	for i, st := range steps {
		st.op()
		if s := p.Snapshot(); (want{s.Applied, s.Flushed, s.Ckpt, s.Total}) != st.want {
			fmt.Printf("FAIL 八步 第%d步: got %+v want %+v\n", i+1, s, st.want)
			ok = false
		}
	}
	check("rec 八步: 每步四元组与推导一致(第5步ckpt=(2,8),第8步total=8)", ok)
	c, err := p.Restart()
	replay := err == nil && c == c28
	for pos, d := range []int64{-1, 6, 2} { // replay positions 3,4,5
		replay = replay && p.Apply(int64(pos)+3, d) == nil
	}
	check("rec 重放: Restart 后重放位点 3..5 得 total=15", replay && p.Snapshot().Total == 15)
}
func checkErrors() {
	p := api.New()
	_ = p.Apply(1, 5)
	p.Flush()
	p.Checkpoint()
	before := p.Snapshot()
	e1 := p.Apply(0, 1)          // non-positive
	e2 := p.Apply(5, 1)          // gap (applied=1)
	_, e3 := api.New().Restart() // no checkpoint
	ok := errors.Is(e1, api.ErrNonPositive) && errors.Is(e2, api.ErrGap) &&
		errors.Is(e3, api.ErrNoCheckpoint) && !errors.Is(e1, api.ErrGap) &&
		!errors.Is(e2, api.ErrNonPositive) && !errors.Is(e3, api.ErrGap) // distinct
	check("api 错误: 三类哨兵错误可判定且互不相同", ok)
	check("api 失败不留痕: 被拒后四元组与 ckpt 全部不变", p.Snapshot() == before)
}

// checkFlushIncremental: flushTotal is the running total, cost independent of m.
func checkFlushIncremental() {
	p := rec.New()
	var sum int64
	for i := 1; i <= 10000; i++ {
		d := int64(i%7 - 3)
		_ = p.Apply(int64(i), d) // sequential positions never fail
		sum += d
	}
	p.Flush()
	s := p.Snapshot()
	check("rec Flush 不重扫: m=10000 直取 running total", s.Flushed == 10000 && s.Total == sum)
}

// checkConcurrent: concurrent Snapshots (plus idempotent Checkpoints) must agree.
func checkConcurrent() {
	p := rec.New()
	for i := 1; i <= 200; i++ {
		_ = p.Apply(int64(i), int64(i))
	}
	p.Flush()
	p.Checkpoint()
	want := p.Snapshot()
	const n = 32
	start, bad := make(chan struct{}), make(chan struct{}, n)
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for i := 0; i < 1000; i++ {
				if g%2 == 1 {
					p.Checkpoint() // idempotent: flushed has not advanced
				}
				if p.Snapshot() != want {
					bad <- struct{}{}
					return
				}
			}
		}(g)
	}
	close(start)
	wg.Wait()
	check("rec 并发: 32 goroutine 并发 Snapshot 逐字段一致", len(bad) == 0)
}

func main() {
	checkCkp()
	checkEightSteps()
	checkErrors()
	checkFlushIncremental()
	checkConcurrent()
	check("api SelfCheck: 内置序列核验四条不变量", api.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
