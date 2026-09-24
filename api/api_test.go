package api

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/inst"
)

func ass(t *testing.T, b bool, m string) {
	if !b {
		t.Fatal(m)
	}
}

// randStep 随机一次 Put/Send（可能附带合法 Deliver）；删除路径由十二步/SelfCheck 覆盖。
func randStep(a *API, r *rand.Rand, P int, sd *[]taggedData) {
	G, vs := a.Versions()
	if n := r.Intn(4); n < 2 {
		_ = a.Publish(put(fmt.Sprintf("r%d", r.Intn(5)), int64(r.Intn(16))))
	} else if n == 2 {
		k, v := r.Int63n(40), int64(r.Intn(20))
		if a.Send(k, v) == nil {
			*sd = append(*sd, taggedData{k, v, G})
		}
	} else if i := r.Intn(P); G > vs[i] {
		_ = a.Deliver(i, 1+r.Intn(G-vs[i]))
	}
}

// watcher 忙等采样各 v 是否始终单调不减；调用方 stop.Store(true) 后 wm.Wait() 再读 mono（无 sleep）。
func watcher(a *API, P int, stop *atomic.Bool, wm *sync.WaitGroup) *bool {
	mono, prev := true, make([]int, P)
	wm.Add(1)
	go func() {
		defer wm.Done()
		for !stop.Load() {
			_, vs := a.Versions()
			for i := range vs {
				mono = mono && vs[i] >= prev[i]
				prev[i] = vs[i]
			}
		}
	}()
	return &mono
}
func TestNaiveEquiv(t *testing.T) {
	for _, c := range [][3]int{{1, 2, 64}, {2, 3, 4}, {3, 5, 128}, {4, 1, 16}} {
		r, a, sd := rand.New(rand.NewSource(int64(c[0]))), New(c[1], c[2]), []taggedData{}
		for s := 0; s < 200; s++ {
			randStep(a, r, c[1], &sd)
		}
		G, vs := a.Versions()
		same := true
		for i := 0; i < c[1]; i++ {
			if G > vs[i] {
				_ = a.Deliver(i, G-vs[i])
			}
			same = same && reflect.DeepEqual(inst.ByInst(a.Output(), i), inst.ByInst(naive(a, sd, c[1]), i))
		}
		ass(t, same && inst.EqualSet(a.Output(), naive(a, sd, c[1])), "naive")
	}
}
func TestTimingIndependence(t *testing.T) {
	c, acc := runTwelve(false)
	late, _ := runTwelve(true)
	same := inst.EqualSet(c.Output(), naive(c, acc, 2)) && inst.EqualSet(c.Output(), late.Output())
	for i := 0; i < 2; i++ {
		same = same && reflect.DeepEqual(inst.ByInst(c.Output(), i), inst.ByInst(late.Output(), i))
	}
	ass(t, same, "timing")
}
func TestInvariants(t *testing.T) {
	a, r, prev, sd := New(3, 7), rand.New(rand.NewSource(7)), []int{0, 0, 0}, []taggedData{}
	for s := 0; s < 300; s++ {
		randStep(a, r, 3, &sd)
		G, vs, bs := a.State()
		for i := 0; i < 3; i++ {
			ass(t, 0 <= vs[i] && vs[i] <= G && vs[i] >= prev[i] && bs[i] <= 7, "invariant")
			prev[i] = vs[i]
		}
	}
	ass(t, New(2, 8).SelfCheck(), "self")
}

func TestRejections(t *testing.T) {
	a := New(2, 8)
	_ = a.Publish(put("r1", 10))
	fs := []func() error{func() error { return a.Publish(del("z")) }, func() error { return a.Publish(put("", 1)) }, func() error { return a.Deliver(0, 2) }, func() error { return a.Deliver(0, 0) }, func() error { return a.Deliver(9, 1) }, func() error { return a.Send(-1, 1) }}
	ws := []error{ErrIllegalRule, ErrIllegalRule, ErrInvalidDeliver, ErrInvalidDeliver, ErrInvalidDeliver, ErrInvalidKey}
	for j := range fs {
		g, v := a.Versions()
		o := a.Output()
		ass(t, fs[j]() == ws[j], "错误类型")
		g2, v2 := a.Versions()
		ass(t, g2 == g && reflect.DeepEqual(v2, v) && inst.EqualSet(o, a.Output()), "留痕")
	}
	ass(t, ErrIllegalRule != ErrInvalidDeliver && ErrIllegalRule != ErrInvalidKey && ErrIllegalRule != ErrBufferFull && ErrInvalidDeliver != ErrInvalidKey && ErrInvalidDeliver != ErrBufferFull && ErrInvalidKey != ErrBufferFull, "互异")
	f := New(1, 1)
	_ = f.Publish(put("r", 100))
	ass(t, f.Send(0, 0) == nil && f.Send(0, 0) == ErrBufferFull, "缓冲满")
	p := New(2, 1)
	st := []func(){func() { _ = p.Publish(put("r1", 10)) }, func() { _ = p.Deliver(0, 1) }, func() { _ = p.Send(0, 15) }, func() { _ = p.Send(1, 12) }, func() { _ = p.Publish(put("r2", 5)) }, func() { _ = p.Publish(del("r1")) }, func() { _ = p.Deliver(1, 3) }, func() { _ = p.Deliver(0, 1) }, func() { _ = p.Deliver(0, 1) }}
	for i := 0; i < 6; i++ {
		st[i]()
	}
	ass(t, p.Send(1, 8) == ErrBufferFull && p.Send(0, 20) == nil, "(丙)7")
	st[6]()
	st[7]()
	ass(t, p.Send(2, 7) == ErrBufferFull, "(丙)11")
	st[8]()
	want := []inst.Hit{{Key: 0, Val: 15, RuleID: "r1", Ver: 1, Inst: 0}, {Key: 1, Val: 12, RuleID: "r1", Ver: 1, Inst: 1}, {Key: 0, Val: 20, RuleID: "r2", Ver: 3, Inst: 0}}
	ass(t, inst.EqualSet(p.Output(), want), "(丙)")
}

func TestConcurrent(t *testing.T) {
	const P, G, N = 4, 20, 2000
	a := New(P, 1<<20)
	for i := 0; i < G; i++ {
		_ = a.Publish(put(fmt.Sprintf("r%d", i%5), int64(i%7)))
	}
	var stop atomic.Bool
	var wm, wg sync.WaitGroup
	mono := watcher(a, P, &stop, &wm)
	for i := 0; i < P; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _ = a.Deliver(i, G) }(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for k := int64(0); k < N; k++ {
			_ = a.Send(k, k%7)
		}
	}()
	wg.Wait()
	stop.Store(true)
	wm.Wait()
	fin, exp := a.log.Snapshot(), []inst.Hit{}
	for k := int64(0); k < N; k++ {
		for _, id := range fin.Match(k % 7) {
			exp = append(exp, inst.Hit{Key: k, Val: k % 7, RuleID: id, Ver: G, Inst: int(k % P)})
		}
	}
	ass(t, *mono && inst.EqualSet(a.Output(), exp), "并发")
}
