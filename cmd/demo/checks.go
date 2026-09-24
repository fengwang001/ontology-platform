package main

import (
	"errors"
	"fmt"
	"time"

	"ontology/attrib"
	"ontology/dump"
	"ontology/sampler"
	"ontology/stack"
	"ontology/tree"
)

func mustNorm(frames []string, depth int) stack.Stack {
	s, err := stack.Normalize(frames, depth)
	if err != nil {
		panic(err)
	}
	return s
}

func checkSelfTotalIdentity() {
	tr := tree.New()
	for _, f := range [][]string{{"m", "A", "B"}, {"m", "A", "C"}, {"m", "A", "B"}, {"m", "D"}} {
		tr.Insert(mustNorm(f, 0))
	}
	report(tr.SumSelf() == tr.Samples && tr.SumTotal() > tr.Samples,
		"self-sum=%d==samples=%d, total-sum=%d>samples", tr.SumSelf(), tr.Samples, tr.SumTotal())
}

func checkTruncation() {
	tr := tree.New()
	deep := make([]string, 12)
	for i := range deep {
		deep[i] = fmt.Sprintf("f%d", i)
	}
	s := mustNorm(deep, 8)
	tr.Insert(s)
	marked := false
	tr.Walk(func(n *tree.Node, _ int) { marked = marked || n.Truncated })
	report(tr.TruncatedSamples == 1 && marked && s.Depth() == 8,
		"truncated-samples=%d marker=%v kept-depth=%d", tr.TruncatedSamples, marked, s.Depth())
}

func checkRecursionAttribution() {
	tr := tree.New()
	for i := 0; i < 7; i++ {
		tr.Insert(mustNorm([]string{"A", "F", "G", "F", "H"}, 0))
	}
	var fTotal, fSelf uint64
	for _, h := range attrib.ByTotal(tr) {
		if h.Frame == "F" {
			fTotal, fSelf = h.Total, h.Self
		}
	}
	report(fTotal == 7 && fSelf == 0,
		"recursion A->F->G->F->H x7: func-total(F)=%d want 7 not 14, self(F)=%d", fTotal, fSelf)
}

// fakeTicker / scriptSource 是可注入的节拍与栈来源。
type fakeTicker struct{ ch chan time.Time }

func newFakeTicker() *fakeTicker             { return &fakeTicker{ch: make(chan time.Time, 4096)} }
func (t *fakeTicker) Chan() <-chan time.Time { return t.ch }
func (t *fakeTicker) Stop()                  { close(t.ch) }
func (t *fakeTicker) kick(n int) {
	for i := 0; i < n; i++ {
		t.ch <- time.Now()
	}
}

type scriptSource struct {
	busyAt map[int]bool
	s      stack.Stack
	i      int
}

func (s *scriptSource) Stack() (stack.Stack, error) {
	i := s.i
	s.i++
	if s.busyAt[i] {
		return stack.Stack{}, sampler.ErrBusy
	}
	return s.s, nil
}

func runDriven(sm *sampler.Sampler, tk *fakeTicker, sent uint64) sampler.Stats {
	go sm.Run()
	for i := 0; i < 10000; i++ {
		st := sm.Stats()
		if st.Ticks+st.BadIntervals >= sent {
			break
		}
		time.Sleep(time.Millisecond)
	}
	sm.Stop()
	sm.Stop() // 停止必须幂等
	return sm.Stats()
}

func checkDropped() {
	tk := newFakeTicker()
	tk.kick(1000)
	busy := map[int]bool{}
	for i := 1; i <= 137; i++ { // 连续忙窗口，恰 137 次丢样
		busy[i] = true
	}
	sm := sampler.New(&scriptSource{busyAt: busy, s: mustNorm([]string{"m", "A"}, 0)},
		tk, func() time.Time { return time.Now() }, time.Millisecond)
	st := runDriven(sm, tk, 1000)
	tr := sm.Snapshot()
	report(st.Dropped == 137 && tr.SumSelf()+st.Dropped == 1000,
		"drop: ticks=%d dropped=%d, self-sum+dropped=%d==1000",
		st.Ticks, st.Dropped, tr.SumSelf()+st.Dropped)
}

type rollbackClock struct {
	t   int64
	hit bool
}

func (c *rollbackClock) now() time.Time {
	if c.t == 2 && !c.hit {
		c.t = 0 // 第 3 次读取：时钟一次性回拨，之后恢复前进
		c.hit = true
	} else {
		c.t++
	}
	return time.Unix(0, c.t*int64(time.Millisecond))
}

func checkClockRollback() {
	tk := newFakeTicker()
	tk.kick(10)
	clk := &rollbackClock{}
	sm := sampler.New(&scriptSource{s: mustNorm([]string{"m", "B"}, 0)},
		tk, clk.now, time.Millisecond)
	st := runDriven(sm, tk, 10)
	tr := sm.Snapshot()
	report(st.BadIntervals == 1 && tr.SumSelf()+st.Dropped == st.Ticks,
		"rollback: bad-intervals=%d, identity self-sum(%d)+dropped(%d)==ticks(%d)",
		st.BadIntervals, tr.SumSelf(), st.Dropped, st.Ticks)
}

func sampleProfile() *dump.Profile {
	tr := tree.New()
	for _, f := range [][]string{{"m", "A", "BB"}, {"m", "A", "C"}, {"m", "A", "BB"}, {"m", "D"}} {
		tr.Insert(mustNorm(f, 0))
	}
	return &dump.Profile{Tree: tr, Dropped: 137}
}

func checkDumpTruncation() {
	full := dump.Encode(sampleProfile())
	_, eHeader := dump.Decode(full[:10])
	_, eRecord := dump.Decode(full[:len(full)-5])
	_, eCRC := dump.Decode(full[:len(full)-1])
	report(errors.Is(eHeader, dump.ErrHeader) && errors.Is(eRecord, dump.ErrRecord) &&
		errors.Is(eCRC, dump.ErrCRC),
		"truncation classes: header=%v record=%v crc=%v", eHeader != nil, eRecord != nil, eCRC != nil)
}

func checkRecoveredTree() {
	full := dump.Encode(sampleProfile())
	var nodes, fullNodes int
	rec, _ := dump.Recover(full[:len(full)-5])
	rec.Tree.Walk(func(*tree.Node, int) { nodes++ })
	recFull, _ := dump.Recover(full)
	recFull.Tree.Walk(func(*tree.Node, int) { fullNodes++ })
	orphanFree := nodes > 0 && nodes < fullNodes
	report(orphanFree && rec.Tree.SumSelf() == rec.Tree.Samples,
		"recover prefix: nodes=%d/%d, self-sum=%d==recovered-samples=%d",
		nodes, fullNodes, rec.Tree.SumSelf(), rec.Tree.Samples)
}

func checkInsertCost() {
	tr := tree.New()
	base := make([]string, 20)
	for i := 0; i < 19; i++ {
		base[i] = fmt.Sprintf("f%02d", i)
	}
	for i := 0; i < 100000; i++ {
		base[19] = []string{"leafA", "leafB"}[i&1]
		tr.Insert(mustNorm(base, 0))
	}
	bound := uint64(100000 * 20 * 4)
	report(tr.Lookups() <= bound,
		"insert 100k depth-20: lookups=%d <= bound=%d", tr.Lookups(), bound)
}
