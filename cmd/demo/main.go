// Command demo 逐条打印 ontology-314 时间索引 Lookup 各项判定，不读参数、不联网。
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"ontology/api"
	"ontology/tlog"
	"os"
	"sync"
	"sync/atomic"
)

var failed bool

func ok(name string, cond bool, detail string) {
	tag := "OK"
	if !cond {
		tag, failed = "FAIL", true
	}
	fmt.Printf("%s %s %s\n", tag, name, detail)
}

func main() {
	s, _ := api.New(100, 1000)
	s.Append([]int64{50, 40, 70, 60, 70, 65, 90, 80, 90, 85})
	es := fmt.Sprint(s.IndexEntries())
	ok("index", es == "[{50 100} {70 102} {90 106}]", es)

	var got []string
	for _, t := range []int64{45, 65, 80, 85, 91} {
		o, f := s.Lookup(t)
		got = append(got, fmt.Sprintf("%d:%d/%v", t, o, f))
	}
	gs := fmt.Sprint(got)
	ok("lookups", gs == "[45:100/true 65:102/true 80:106/true 85:106/true 91:110/false]", gs)

	naive, mono := true, true
	r := rand.New(rand.NewSource(7))
	for k := 0; k < 20 && naive && mono; k++ {
		q, _ := api.New(0, 100000)
		seq := make([]int64, r.Intn(200)+1)
		for i := range seq {
			seq[i] = int64(r.Intn(50))
		}
		q.Append(seq)
		var prev int64 = -1
		for t := int64(-1); t <= 51; t++ {
			o, f := q.Lookup(t)
			wo, wf := q.LEO(), false
			for i, x := range seq {
				if x >= t {
					wo, wf = int64(i), true
					break
				}
			}
			if o != wo || f != wf {
				naive = false
			}
			if o < prev {
				mono = false
			}
			prev = o
		}
	}
	ok("naive-reference(random)", naive, "")
	ok("monotonic", mono, "")

	_, e1 := api.New(-1, 1)
	_, e2 := api.New(0, 0)
	st, _ := api.New(0, 5)
	st.Append([]int64{1, 2})
	_, en := st.Append([]int64{-1})
	_, ec := st.Append([]int64{1, 2, 3, 4})
	distinct := tlog.ErrInvalidParam != tlog.ErrNegativeTS && tlog.ErrNegativeTS != tlog.ErrCapacity
	ok("sentinel-errors",
		errors.Is(e1, tlog.ErrInvalidParam) && errors.Is(e2, tlog.ErrInvalidParam) &&
			errors.Is(en, tlog.ErrNegativeTS) && errors.Is(ec, tlog.ErrCapacity) && distinct, "")

	noTrace := st.LEO() == 2 && len(st.IndexEntries()) == 2
	if f, err := st.Append([]int64{3}); err != nil || f != 2 {
		noTrace = false
	}
	ok("rejected-no-trace", noTrace, fmt.Sprintf("LEO=%d", st.LEO()))

	bounded := true
	for _, m := range []int{100, 1000, 10000} {
		b, _ := api.New(0, m)
		ts := make([]int64, m)
		for i := range ts {
			ts[i] = int64(i) * 2
		}
		b.Append(ts)
		for _, t := range []int64{-1, int64(m) * 2, 50, 51} {
			b.Lookup(t)
			if !b.ProbeWithinBound() {
				bounded = false
			}
		}
	}
	ok("probes-bounded(m<=10000)", bounded, "")
	ok("concurrent-stable-hit", concurrentStable(), "")
	ok("selfcheck", s.SelfCheck() == nil, "")

	if failed {
		os.Exit(1)
	}
}

func concurrentStable() bool {
	q, _ := api.New(0, 100000)
	var wg sync.WaitGroup
	start, done := make(chan struct{}), make(chan struct{})
	const target int64 = 500
	var fixed atomic.Int64
	fixed.Store(-1)
	var good atomic.Bool
	good.Store(true)
	wg.Add(4)
	for range 4 {
		go func() {
			defer wg.Done()
			<-start
			for {
				if o, f := q.Lookup(target); f {
					if v := fixed.Load(); v == -1 {
						fixed.CompareAndSwap(-1, o)
					} else if o != v {
						good.Store(false)
					}
				}
				select {
				case <-done:
					return
				default:
				}
			}
		}()
	}
	close(start)
	for i := 0; i < 100; i++ {
		q.Append([]int64{int64(i) * 10})
	}
	close(done)
	wg.Wait()
	return good.Load() && fixed.Load() == 50
}
