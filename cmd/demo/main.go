package main

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/hist"
	"ontology/lc"
)

// builtIn replays the nine-event sequence (n=3): m1 3->1, m2 1->2,
// m3 2->3; ops are {0 local x, 1 send x->y, 2 recv x}.
func builtIn() (*hist.History, error) {
	h, err := hist.New(3, 64)
	if err != nil {
		return nil, err
	}
	ops := [][3]int{{0, 3, 0}, {1, 3, 1}, {0, 2, 0}, {2, 1, 0}, {1, 1, 2}, {1, 2, 3}, {2, 2, 0}, {2, 3, 0}, {0, 1, 0}}
	for _, o := range ops {
		switch o[0] {
		case 0:
			_, err = h.Local(o[1])
		case 1:
			_, _, err = h.Send(o[1], o[2])
		case 2:
			_, err = h.Recv(o[1])
		}
		if err != nil {
			return nil, err
		}
	}
	return h, nil
}

func main() {
	fails := 0
	check := func(name string, ok bool) {
		if ok {
			fmt.Println("OK:", name)
		} else {
			fmt.Println("FAIL:", name)
			fails++
		}
	}

	var c lc.Clock
	check("lc tick/send L=L+1", c.Tick() == 1 && c.Tick() == 2)
	var r lc.Clock
	check("lc recv L=max(L,t)+1", r.Recv(2) == 3 && r.Recv(4) == 5 && r.Recv(2) == 6)
	check("lc total order (ts,node)",
		lc.Less(2, 3, 3, 1) && !lc.Less(3, 1, 2, 3) &&
			lc.Less(3, 1, 3, 2) && lc.Less(1, 3, 2, 3))

	h, err := builtIn()
	check("builtin nine events built", err == nil)
	wantTS := []int64{0, 1, 2, 1, 3, 4, 2, 5, 3, 5}
	got := h.Order()
	wantOrder := []int{3, 1, 6, 2, 4, 8, 5, 9, 7}
	tsOK, ordOK := true, len(got) == 9
	for _, e := range got {
		if e.TS != wantTS[e.ID] {
			tsOK = false
		}
	}
	for i, id := range wantOrder {
		if i >= len(got) || got[i].ID != id {
			ordOK = false
		}
	}
	check("nine-event timestamps and total order", tsOK && ordOK)
	check("e6 and e4 are concurrent", !h.HappensBefore(6, 4) && !h.HappensBefore(4, 6))
	check("order==batch, clock condition, strict order", h.SelfCheck() == nil)
	check("insert comparisons stay logarithmic at large m", hist.CheckInsertBound() == nil)

	s, _ := api.New(3, 100)
	_, eBadNew := api.New(0, 10)
	_, eNode := s.Local(9)
	_, _, eSend := s.Send(1, 9)
	_, eNoMsg := s.Recv(999)
	noTrace := len(s.Order()) == 0 // all above rejected: nothing recorded
	mid, mts, eSendOK := s.Send(1, 2)
	_, eRecv := s.Recv(mid)
	_, eDup := s.Recv(mid)
	lim, _ := api.New(2, 2)
	_, _ = lim.Local(1)
	_, _ = lim.Local(2)
	_, eLimit := lim.Local(1)
	nextTS, stillOK := s.Local(1) // s survived its earlier rejections
	distinct := errors.Is(eBadNew, api.ErrNode) && errors.Is(eNode, api.ErrNode) &&
		errors.Is(eSend, api.ErrNode) && errors.Is(eNoMsg, api.ErrNoMessage) &&
		errors.Is(eRecv, nil) && errors.Is(eDup, api.ErrAlreadyRecv) &&
		errors.Is(eLimit, api.ErrLimit) && errors.Is(stillOK, nil) &&
		mid == 1 && mts == 1 && eSendOK == nil && noTrace && nextTS == 2 &&
		len(s.Order()) == 3
	check("four distinct decidable errors; rejected ops leave no trace", distinct)

	n, K := 4, 500
	p, _ := api.New(n, n*K)
	var wg sync.WaitGroup
	for node := 1; node <= n; node++ {
		wg.Add(1)
		go func(node int) {
			defer wg.Done()
			for i := 0; i < K; i++ {
				if _, e := p.Local(node); e != nil {
					panic(e)
				}
			}
		}(node)
	}
	wg.Wait()
	readers := make([][]api.Event, 8)
	var rwg sync.WaitGroup
	for i := range readers {
		rwg.Add(1)
		go func(i int) { defer rwg.Done(); readers[i] = p.Order() }(i)
	}
	rwg.Wait()
	same := true
	for _, o := range readers[1:] {
		if !reflect.DeepEqual(readers[0], o) {
			same = false
		}
	}
	check("concurrent writers/readers: n*K events, batch-consistent, equal reads",
		len(p.Order()) == n*K && p.SelfCheck() == nil && same)

	if fails > 0 {
		panic("demo checks failed")
	}
}
