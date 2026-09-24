// Command demo 逐条验证限速回放器关键性质，退出码 0 表示全部通过。
package main

import (
	"fmt"
	"os"
	"sync"

	"ontology/api"
)

var fails int

func rng(lo, hi int64) []int64 {
	s := make([]int64, 0, hi-lo+1)
	for i := lo; i <= hi; i++ {
		s = append(s, i)
	}
	return s
}
func chk(tag, detail string, ok bool) {
	if !ok {
		fails++
	}
	st := "OK"
	if !ok {
		st = "FAIL"
	}
	fmt.Printf("%s %s %s\n", st, tag, detail)
}
func main() {
	// 1) 第三节八步：逐步记录 now/tokens/pending/吐出。
	p, _ := api.New(2, 5, 10, 100)
	var trace string
	note := func(out []int64) {
		trace += fmt.Sprintf("n=%d,t=%d,q=%d,out=%v | ", p.Now(), p.Tokens(), p.Pending(), out)
	}
	_ = p.Enqueue(rng(1, 15)...)
	note(nil)
	e2 := p.Emit()
	note(e2)
	_ = p.Advance(1)
	note(nil)
	e4 := p.Emit()
	note(e4)
	_ = p.Advance(2)
	note(nil)
	note(p.Emit())
	_ = p.Enqueue(16, 17, 18)
	note(nil)
	e8 := p.Emit()
	note(e8)
	chk("8steps", trace, p.Now() == 2 && p.Tokens() == 0 && p.Pending() == 1 &&
		len(e2) == 10 && len(e4) == 5 && len(e8) == 2)
	// 2) 甲：第3步队列非空按 cap 补 5；忽略追赶恒按 rate 只补 2、第4步吐[11 12]、剩[13 14 15]。
	chk("jia", "refill@3=cap*1=5 emit4=5; wrong(always rate): refill=2 emit=[11 12] left=[13 14 15]", len(e4) == 5)
	// 3) 乙：burst 把瞬时突发封顶在 10；桶若无限第2步会吐 15。
	chk("yi", "burst=10 flattens: step2 emitted 10 not 15; infinite burst would emit 15", len(e2) == 10)
	// 4) 丙：再 Advance(3)（dt=1、非空→cap 补5）后 18 被吐出；cap 错当次数上限则第2步错吐 5。
	_ = p.Advance(3)
	eP := p.Emit()
	chk("bing", "18 emitted; cap-as-count-limit would wrongly emit 5 at step2", len(eP) == 1 && eP[0] == 18)
	// 5) 保序与守恒：交叉入队/推进/吐出，严格按序且 in==out+pending。
	q, _ := api.New(2, 5, 10, 1000)
	var next, in, out int64 = 1, 0, 0
	ordered := true
	run := func(ids []int64, adv int64) {
		_ = q.Enqueue(ids...)
		in += int64(len(ids))
		_ = q.Advance(adv)
		for _, id := range q.Emit() {
			if id != next {
				ordered = false
			}
			next, out = next+1, out+1
		}
	}
	run(rng(1, 20), 1)
	run(rng(21, 40), 3)
	run(rng(41, 45), 2)
	chk("order+conservation", fmt.Sprintf("next=%d in=%d out+pending=%d", next, in, out+int64(q.Pending())),
		ordered && in == out+int64(q.Pending()))
	// 6) 三类哨兵互不相同；被拒后状态不变且实例仍可用。
	a, _ := api.New(2, 5, 10, 2)
	_ = a.Enqueue(1, 2)
	n0, t0, p0 := a.Now(), a.Tokens(), a.Pending()
	_, eBad := api.New(0, 5, 10, 2)
	eRoll := a.Advance(-1)
	eFull := a.Enqueue(3)
	distinct := eBad != eRoll && eRoll != eFull && eBad != eFull
	untraced := a.Now() == n0 && a.Tokens() == t0 && a.Pending() == p0
	_ = a.Advance(1)
	chk("errors+no-trace", fmt.Sprintf("distinct=%v untraced=%v usable=%v", distinct, untraced, len(a.Emit()) == 2),
		distinct && untraced && eBad == api.ErrInvalidParam && eRoll == api.ErrClockRollback && eFull == api.ErrQueueFull)
	// 7) 大 m：tokens 足够一次吐完、tokens=1 只吐 1；判定只经 len 与切片（计数器恒0由 pace 同包测试钉住）。
	bigOK := true
	for _, m := range []int{100, 1000, 10000} {
		ids := make([]int64, m)
		for i := range ids {
			ids[i] = int64(i + 1)
		}
		b, _ := api.New(1, int64(m), int64(m), m)
		_ = b.Enqueue(ids...)
		full := b.Emit()
		c, _ := api.New(1, int64(m), 1, m)
		_ = c.Enqueue(ids...)
		one := c.Emit()
		if len(full) != m || full[m-1] != int64(m) || len(one) != 1 || one[0] != 1 {
			bigOK = false
		}
	}
	chk("big-m O(1) decision", "m=100/1000/10000 one-shot; tokens=1 emits 1", bigOK)
	// 8) 并发只读：N 个 goroutine 读同一喂满实例，所见三元组逐字段相同（不用 sleep）。
	r, _ := api.New(3, 7, 9, 100)
	_ = r.Enqueue(rng(1, 50)...)
	_ = r.Advance(4)
	_ = r.Emit()
	type snap struct {
		p    int
		t, n int64
	}
	res := make(chan snap, 1600)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				res <- snap{r.Pending(), r.Tokens(), r.Now()}
			}
		}()
	}
	wg.Wait()
	close(res)
	base, same := <-res, true
	for s := range res {
		if s != base {
			same = false
		}
	}
	chk("concurrent readers", fmt.Sprintf("%v", base), same)
	// 9) 内置自检（四条不变量）。
	chk("SelfCheck", "naive/cap/conservation/no-trace", p.SelfCheck() == nil)
	if fails > 0 {
		os.Exit(1)
	}
}
