package main

import (
	"errors"
	"fmt"
	"log"
	"math/rand"
	"sync"

	"ontology/api"
	"ontology/hlc"
	"ontology/trace"
)

func checkTwelve() error {
	a, _ := api.New(2, 5, 100)
	steps := []struct {
		node int
		pt   int64
		kind byte // 'l' local, 's' send, 'r' recv
		want hlc.T
	}{
		{0, 10, 's', hlc.T{L: 10, C: 0}}, {1, 11, 'r', hlc.T{L: 11, C: 0}},
		{1, 12, 's', hlc.T{L: 12, C: 0}}, {1, 11, 'l', hlc.T{L: 12, C: 1}},
		{1, 12, 'l', hlc.T{L: 12, C: 2}}, {1, 12, 'l', hlc.T{L: 12, C: 3}},
		{0, 7, 'r', hlc.T{L: 12, C: 1}}, {0, 12, 's', hlc.T{L: 12, C: 2}},
		{1, 12, 'r', hlc.T{L: 12, C: 4}}, {1, 12, 's', hlc.T{L: 12, C: 5}},
		{0, 15, 'l', hlc.T{L: 15, C: 0}}, {0, 14, 'r', hlc.T{L: 15, C: 1}},
	}
	var ids []int64
	for i, s := range steps {
		var got hlc.T
		var err error
		switch s.kind {
		case 'l':
			got, err = a.Local(s.node, s.pt)
		case 's':
			var id int64
			got, id, err = a.Send(s.node, 1-s.node, s.pt)
			ids = append(ids, id)
		case 'r':
			got, err = a.Recv(s.node, ids[0], s.pt)
			ids = ids[1:]
		}
		if err != nil || got != s.want {
			return fmt.Errorf("step %d: got %v,%v want %v", i+1, got, err, s.want)
		}
	}
	return nil
}

func checkErrors() error {
	a, _ := api.New(2, 5, 100)
	_, id, _ := a.Send(0, 1, 10)
	_, e1 := a.Local(5, 1)
	_, e2 := a.Recv(1, 99, 4)
	_, e3 := a.Recv(1, id, 4) // 10-4>5: offset
	small, _ := api.New(1, 5, 1)
	_, _ = small.Local(0, 1)
	_, _ = small.Local(0, 1)
	_, e4 := small.Local(0, 1) // counter
	if !errors.Is(e1, trace.ErrParam) || !errors.Is(e2, trace.ErrNoMsg) ||
		!errors.Is(e3, hlc.ErrOffset) || !errors.Is(e4, hlc.ErrCounter) {
		return fmt.Errorf("wrong classes: %v %v %v %v", e1, e2, e3, e4)
	}
	if n, _ := a.CountUpTo(1, 1<<62, 1<<62); n != 0 {
		return errors.New("rejected recv mutated state")
	}
	_, err := a.Recv(1, id, 5) // 10-5==maxOffset: accepted
	return err
}

func checkCount() error {
	a, _ := api.New(1, 5, 1<<40)
	rng := rand.New(rand.NewSource(7))
	var ts []hlc.T
	pt := int64(0)
	for range 10000 {
		pt = max(0, pt+rng.Int63n(21)-10) // random walk with rollback
		t, err := a.Local(0, pt)
		if err != nil {
			return err
		}
		ts = append(ts, t)
	}
	for _, q := range []int{1234, 5000, 9999} {
		got, err := a.CountUpTo(0, ts[q].L, ts[q].C)
		if err != nil || got != q+1 {
			return fmt.Errorf("CountUpTo=%d,%v want %d", got, err, q+1)
		}
	}
	return nil
}

func checkConcurrent() error {
	tr, _ := trace.New(8, 1000, 1<<40)
	inbox := make([][]int64, 8)
	var mu sync.Mutex
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for me := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 40 {
				pt := int64(j*3+me) - int64(j%5)*2 // includes rollback
				if _, errs[me] = tr.Local(me, pt); errs[me] != nil {
					return
				}
				_, id, err := tr.Send(me, (me+1)%8, pt)
				if err != nil {
					errs[me] = err
					return
				}
				mu.Lock()
				inbox[(me+1)%8] = append(inbox[(me+1)%8], id)
				ids := inbox[me]
				inbox[me] = nil
				mu.Unlock()
				for _, id2 := range ids {
					if _, errs[me] = tr.Recv(me, id2, pt); errs[me] != nil {
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	return errors.Join(errors.Join(errs...), tr.Verify())
}

func main() {
	self, _ := api.New(1, 1, 1)
	checks := []struct {
		name string
		fn   func() error
	}{
		{"twelve-step (l,c) match NOTES.md; step7 offset==maxOffset accepted", checkTwelve},
		{"SelfCheck: naive-ref, strictly-increasing, causal, failure-atomic", self.SelfCheck},
		{"four error classes distinct; rejected recv leaves state, later received", checkErrors},
		{"CountUpTo exact at m=10000 (log bound pinned by TestCountUpToComparisons)", checkCount},
		{"concurrent events verified", checkConcurrent},
	}
	for _, c := range checks {
		if err := c.fn(); err != nil {
			log.Fatalf("FAIL %s: %v", c.name, err)
		}
		fmt.Println("OK", c.name)
	}
}
