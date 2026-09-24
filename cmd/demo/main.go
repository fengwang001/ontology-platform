package main

import (
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"slices"
	"sync"

	"ontology/api"
	"ontology/cbuf"
	"ontology/vc"
)

func runNaive(n int, arr []api.Msg) (loc []int64, buf, del []api.Msg, dups int64) {
	loc, have := make([]int64, n), map[[2]int64]bool{}
	for _, m := range arr {
		k := [2]int64{int64(m.From), m.V[m.From]}
		if have[k] || m.V[m.From] <= loc[m.From] {
			dups++
			continue
		}
		have[k] = true
		if !vc.Deliverable(m, loc) {
			buf = append(buf, m)
			continue
		}
		loc[m.From]++
		del = append(del, m)
		for {
			idx := -1
			for i, q := range buf {
				if vc.Deliverable(q, loc) && (idx < 0 || q.From < buf[idx].From) {
					idx = i
				}
			}
			if idx < 0 {
				break
			}
			loc[buf[idx].From]++
			del = append(del, buf[idx])
			buf = append(buf[:idx], buf[idx+1:]...)
		}
	}
	return loc, buf, del, dups
}

func closedSet(n, k int) []api.Msg {
	ms := make([]api.Msg, n*k)
	for p := range ms {
		j, t := p/k, p%k+1
		v := slices.Repeat([]int64{int64(t - 1)}, n)
		v[j] = int64(t)
		ms[p] = api.Msg{From: j, V: v}
	}
	return ms
}

func main() {
	fails, eq := 0, reflect.DeepEqual
	ok := func(n string, g bool) {
		t := "OK  "
		if !g {
			t, fails = "FAIL ", fails+1
		}
		fmt.Println(t + n)
	}
	mk := func(f int, v ...int64) api.Msg { return api.Msg{From: f, V: v} }
	a, b, c := mk(0, 1, 0, 0), mk(1, 1, 1, 0), mk(0, 2, 0, 0)
	d, e, f := mk(2, 1, 1, 1), mk(1, 2, 2, 0), mk(2, 2, 2, 2)
	r, _ := api.New(3, 8)
	seven := []api.Msg{e, d, b, c, f, a, b}
	stepOK := true
	for i, m := range seven {
		_, err := r.Receive(m)
		l, bf, dl, dp := runNaive(3, seven[:i+1])
		if err != nil || !eq(r.Delivered(), dl) || !eq(r.Local(), l) || len(r.Buffered()) != len(bf) || r.Dups() != dp {
			stepOK = false
		}
	}
	ok("seven steps: per-step delivered & V_local", stepOK)
	ok("step6 cascade order a,c,b,e,d,f", eq(r.Delivered(), []api.Msg{a, c, b, e, d, f}))
	ok("step7 duplicate b dropped", r.Dups() == 1 && len(r.Buffered()) == 0)
	rnd := rand.New(rand.NewSource(1))
	agree := true
	for it := 0; it < 30; it++ {
		set := closedSet(3, 3)
		arr := append(append([]api.Msg{}, set...), set[:4]...)
		rnd.Shuffle(len(arr), func(i, j int) { arr[i], arr[j] = arr[j], arr[i] })
		g, _ := api.New(3, 64)
		for _, m := range arr {
			g.Receive(m)
		}
		l, bf, dl, dp := runNaive(3, arr)
		if !eq(g.Delivered(), dl) || !eq(g.Local(), l) || len(g.Buffered()) != len(bf) || g.Dups() != dp {
			agree = false
		}
	}
	ok("random arrivals agree with naive reference", agree)
	_, ep := api.New(0, 1)
	_, en := api.New(2, -1)
	g2, _ := api.New(2, 1)
	snap := func() string { return fmt.Sprint(g2.Local(), g2.Buffered(), g2.Delivered(), g2.Dups()) }
	s0 := snap()
	var ee [2]error
	for i, m := range []api.Msg{mk(9, 0, 0), mk(0, 2)} {
		_, ee[i] = g2.Receive(m)
	}
	noTrace := snap() == s0
	g2.Receive(mk(1, 1, 1))
	_, ef := g2.Receive(mk(0, 2, 0))
	_, eu := g2.Receive(mk(0, 1, 0))
	errOK := ep == api.ErrParam && en == api.ErrParam && ee[0] == api.ErrSender && ee[1] == api.ErrVector && ef == api.ErrFull
	ok("four distinct decidable sentinel errors", errOK)
	ok("reject leaves no trace, still usable", noTrace && eu == nil)
	ok("checked-count flat in m, tight on cascade", cbuf.ComplexityBoundOK())
	set := closedSet(3, 4)
	arr := append(append([]api.Msg{}, set...), set[:6]...)
	rnd.Shuffle(len(arr), func(i, j int) { arr[i], arr[j] = arr[j], arr[i] })
	gc, _ := api.New(3, 128)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := w; i < len(arr); i += 8 {
				gc.Receive(arr[i])
			}
		}(w)
	}
	close(start)
	wg.Wait()
	dl := gc.Delivered()
	caus := true
	for i := range dl {
		for k := i + 1; k < len(dl); k++ {
			if vc.Before(dl[k].V, dl[i].V) {
				caus = false
			}
		}
	}
	ok("concurrent: all once, causal, dups=6", len(dl) == 12 && len(gc.Buffered()) == 0 && gc.Dups() == 6 && caus)
	if fails > 0 {
		os.Exit(1)
	}
}
