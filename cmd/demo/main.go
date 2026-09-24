package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"sync"

	"ontology/api"
	"ontology/hist"
	"ontology/lc"
)

var failed bool

func ok(name string, cond bool) {
	if cond {
		fmt.Println("OK", name)
	} else {
		fmt.Println("FAIL", name)
		failed = true
	}
}

func nine(s *api.System) []int { // 执行第三节九个事件，返回各事件时间戳（下标即事件 ID）
	ts := make([]int, 10)
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	must(s.Local(3)) // e1
	m1, v2, _ := s.Send(3, 1)
	ts[2] = int(v2)  // e2
	must(s.Local(2)) // e3
	v4, _ := s.Recv(m1)
	ts[4] = int(v4) // e4
	m2, v5, _ := s.Send(1, 2)
	ts[5] = int(v5) // e5
	m3, v6, _ := s.Send(2, 3)
	ts[6] = int(v6) // e6
	v7, _ := s.Recv(m2)
	ts[7] = int(v7) // e7
	v8, _ := s.Recv(m3)
	ts[8] = int(v8)  // e8
	must(s.Local(1)) // e9
	ts[1], ts[3], ts[9] = 1, 1, 5
	return ts
}

func main() {
	c := lc.New(1)
	t1, t2 := c.Local(), c.Send()
	r := lc.New(2)
	t3 := r.Recv(t2)
	ok("lc: clock rules (1,2,3) and (ts,node) order",
		t1 == 1 && t2 == 2 && t3 == 3 &&
			lc.Less(lc.MakeKey(1, 2), lc.MakeKey(1, 3)) && lc.Less(lc.MakeKey(1, 2), lc.MakeKey(2, 1)))

	s, _ := api.New(3, 100)
	ts := nine(s)
	ord := s.Order()
	ids := make([]int, 9)
	byID := make([]hist.Event, 10)
	for i, e := range ord {
		ids[i] = e.ID
		byID[e.ID] = e
	}
	ok("nine events: timestamps 1,2,1,3,4,2,5,3,5 and total order e3,e1,e6,e2,e4,e8,e5,e9,e7",
		slices.Equal(ts[1:], []int{1, 2, 1, 3, 4, 2, 5, 3, 5}) &&
			slices.Equal(ids, []int{3, 1, 6, 2, 4, 8, 5, 9, 7}))
	ok("e6 and e4 are concurrent", !s.HappensBefore(6, 4) && !s.HappensBefore(4, 6))

	batch := append([]hist.Event(nil), ord...)
	sort.Slice(batch, func(i, j int) bool {
		if batch[i].TS != batch[j].TS {
			return batch[i].TS < batch[j].TS
		}
		return batch[i].Node < batch[j].Node
	})
	ok("Order() equals batch recompute", slices.Equal(ord, batch))

	clockOK := true
	for a := 1; a <= 9; a++ {
		for b := 1; b <= 9; b++ {
			if s.HappensBefore(a, b) && !(byID[a].TS < byID[b].TS) {
				clockOK = false
			}
		}
	}
	ok("clock condition holds for every happens-before pair", clockOK)

	errOK := errors.Is(s.Local(9), hist.ErrInvalidNode) &&
		errors.Is(func() error { _, _, e := s.Send(0, 1); return e }(), hist.ErrInvalidNode) &&
		errors.Is(func() error { _, e := s.Recv(999); return e }(), hist.ErrMsgNotFound) &&
		errors.Is(func() error { _, e := s.Recv(1); return e }(), hist.ErrMsgRecvTwice)
	g, _ := api.New(1, 1)
	_ = g.Local(1)
	errOK = errOK && errors.Is(g.Local(1), hist.ErrEventLimit)
	ok("four distinct decidable errors", errOK)
	ok("rejections leave no trace; system still usable",
		len(s.Order()) == 9 && len(g.Order()) == 1 && s.Local(1) == nil)

	ok("insert comparisons bounded by 2*ceil(log2(m+1))+2 for m=100..10000",
		hist.LogInsertBoundHolds(100) && hist.LogInsertBoundHolds(1000) && hist.LogInsertBoundHolds(10000))

	n, k := 8, 50
	sys, _ := api.New(n, n*k)
	var wg sync.WaitGroup
	for node := 1; node <= n; node++ {
		wg.Add(1)
		go func(nd int) {
			defer wg.Done()
			for i := 0; i < k; i++ {
				_ = sys.Local(nd)
			}
		}(node)
	}
	wg.Wait()
	per := map[int][]int{}
	for _, e := range sys.Order() {
		per[e.Node] = append(per[e.Node], e.TS)
	}
	stampsOK := len(sys.Order()) == n*k
	for nd := 1; nd <= n; nd++ {
		for i, t := range per[nd] {
			stampsOK = stampsOK && t == i+1
		}
	}
	first := sys.Order()
	ch := make(chan bool, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); ch <- slices.Equal(sys.Order(), first) }()
	}
	wg.Wait()
	close(ch)
	readersOK := true
	for v := range ch {
		readersOK = readersOK && v
	}
	ok("concurrent writers keep stamps 1..K; concurrent readers see identical order",
		stampsOK && readersOK)

	if failed {
		os.Exit(1)
	}
}
