package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/part"
	"ontology/router"
)

var failed bool

func ok(name string, cond bool) {
	if cond {
		fmt.Println("OK  " + name)
	} else {
		failed = true
		fmt.Println("FAIL " + name)
	}
}

func main() {
	p := part.New(7)
	p.Add("k")
	ok("part state machine Active->Draining->Removed",
		p.BeginDrain() && p.State() == part.Draining && !p.BeginDrain() &&
			p.MarkRemoved() && p.State() == part.Removed && p.Count() == 0)

	a := api.New()
	type step struct {
		run    func() error
		reject bool
		counts [3]int
	}
	steps := []step{ // 第三节八步
		{func() error { return a.Assign("a", 0) }, false, [3]int{1, 0, 0}},
		{func() error { return a.Assign("b", 0) }, false, [3]int{2, 0, 0}},
		{func() error { return a.Assign("c", 1) }, false, [3]int{2, 1, 0}},
		{func() error { return a.Assign("d", 2) }, false, [3]int{2, 1, 1}},
		{func() error { return a.BeginDrain(2) }, false, [3]int{2, 1, 1}},
		{func() error { return a.Assign("e", 2) }, true, [3]int{2, 1, 1}},
		{func() error { return a.Put("d", "x") }, true, [3]int{2, 1, 1}},
		{func() error { return a.Migrate(2, 0) }, false, [3]int{3, 1, 0}},
	}
	good := true
	var prog strings.Builder
	for i, s := range steps {
		err := s.run()
		good = good && (err != nil) == s.reject &&
			a.Count(0) == s.counts[0] && a.Count(1) == s.counts[1] && a.Count(2) == s.counts[2]
		if i > 0 {
			prog.WriteByte('|')
		}
		fmt.Fprintf(&prog, "%d,%d,%d", a.Count(0), a.Count(1), a.Count(2))
		if err != nil {
			prog.WriteByte('!')
		}
	}
	ok("eight-step counts P0,P1,P2 per step (!=rejected): "+prog.String(), good)

	v, exists := a.Get("d")
	ok("post-migrate Get(d) empty; d now writable via Active P0",
		!exists && v == "" && a.Put("d", "x") == nil)

	sum := a.Count(0) + a.Count(1) + a.Count(2)
	ok("counts match batch recompute (accepted assigns=4, sum=4)",
		a.Count(0) == 3 && a.Count(1) == 1 && a.Count(2) == 0 && sum == 4)

	b := api.New()
	b.Assign("k", 0)
	b.BeginDrain(1)
	errs := []error{b.Assign("x", 9), b.Assign("y", 1), b.Put("zz", "v"), b.Migrate(0, 0)}
	sents := []error{router.ErrNoSuchPartition, router.ErrAffinityConflict, router.ErrNoAffinity, router.ErrBadMigrate}
	distinct := true
	for i := range sents {
		distinct = distinct && errors.Is(errs[i], sents[i])
		for j := range sents {
			distinct = distinct && (i == j || !errors.Is(errs[i], sents[j]))
		}
	}
	ok("four decidable sentinel errors, mutually distinct", distinct)

	c0, c1, c2 := b.Count(0), b.Count(1), b.Count(2)
	b.Migrate(0, 1) // from 非 Draining，被拒
	b.Assign("k", 0)
	b.Put("ghost", "v")
	ok("rejected ops leave no trace",
		b.Count(0) == c0 && b.Count(1) == c1 && b.Count(2) == c2)

	ok("migrate scan count independent of m (100..10000)", router.CheckScanBound() == nil)

	c := api.New()
	const K = 50
	for i := 0; i < K; i++ {
		key := fmt.Sprintf("k%d", i)
		c.Assign(key, 2)
		c.Put(key, "v")
	}
	c.BeginDrain(2)
	done := make(chan struct{})
	var wg sync.WaitGroup
	var torn int32
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				n2, n0 := c.Count(2), c.Count(0)
				if !(n2 == 0 || n2 == K) || !(n0 == 0 || n0 == K) {
					atomic.StoreInt32(&torn, 1)
					return
				}
				for i := 0; i < K; i++ {
					if v, ok := c.Get(fmt.Sprintf("k%d", i)); !ok || v != "v" {
						atomic.StoreInt32(&torn, 1)
						return
					}
				}
			}
		}()
	}
	c.Migrate(2, 0)
	close(done)
	wg.Wait()
	ok("concurrent readers consistent; migrate atomic (no torn state)",
		torn == 0 && c.Count(2) == 0 && c.Count(0) == K)

	ok("SelfCheck", api.New().SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
