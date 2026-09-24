// Command demo verifies the semi-join invariants end to end.
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/key"
	"ontology/semi"
)

var failed bool

func check(name string, ok bool) {
	s := "OK"
	if !ok {
		s, failed = "FAIL", true
	}
	fmt.Println(name+":", s)
}

func sp(s string) *string { return &s }

// steps replays the eight ops from NOTES.md, checking the view after each.
func steps() bool {
	e, _ := semi.New(8)
	a, b := sp("a"), sp("b")
	ops := []func() error{
		func() error { return e.AddLeft(1, a) },
		func() error { return e.AddLeft(2, a) },
		func() error { return e.AddRight(a) },
		func() error { return e.AddLeft(3, b) },
		func() error { return e.AddRight(b) },
		func() error { return e.AddLeft(4, nil) },
		func() error { return e.DelRight(a) },
		func() error { return e.AddRight(nil) },
	}
	want := [][]int64{{}, {}, {1, 2}, {1, 2}, {1, 2, 3}, {1, 2, 3}, {3}, {3}}
	for i, op := range ops {
		if op() != nil || !slices.Equal(e.View(), want[i]) {
			return false
		}
	}
	return true
}

func semantics() (noDup, nullOK bool) {
	e, _ := semi.New(8)
	_ = e.AddLeft(1, sp("a"))
	_ = e.AddLeft(2, sp("a"))
	_ = e.AddRight(sp("a"))
	_ = e.AddRight(sp("a")) // ref 1->2: view must not change
	noDup = slices.Equal(e.View(), []int64{1, 2})
	_ = e.AddLeft(4, nil)
	_ = e.AddRight(nil)
	return noDup, slices.Equal(e.View(), []int64{1, 2})
}

func errorsDistinct() bool {
	_, err0 := semi.New(0)
	e, _ := semi.New(1)
	_ = e.AddLeft(1, sp("a"))
	errs := []error{err0, e.AddLeft(2, sp("b")), e.AddLeft(1, sp("b")), e.DelLeft(99), e.DelRight(sp("z"))}
	want := []error{semi.ErrBadMaxLeft, semi.ErrTooManyLeft, semi.ErrLeftExists, semi.ErrLeftNotFound, semi.ErrRefNegative}
	for i := range errs {
		if !errors.Is(errs[i], want[i]) {
			return false
		}
	}
	return true
}

func noTrace() bool {
	e, _ := semi.New(2)
	_ = e.AddLeft(1, sp("a"))
	_ = e.AddLeft(2, sp("b"))
	_ = e.AddRight(sp("a"))
	before := e.View()
	rejects := []error{ // dup id, over limit, missing id, negative ref, NULL negative
		e.AddLeft(1, sp("x")), e.AddLeft(3, sp("x")), e.DelLeft(99),
		e.DelRight(sp("z")), e.DelRight(nil),
	}
	for _, err := range rejects {
		if err == nil {
			return false
		}
	}
	if !slices.Equal(e.View(), before) || !slices.Equal(e.BatchView(), before) {
		return false
	}
	return e.AddRight(sp("z")) == nil && e.DelRight(sp("z")) == nil && e.DelLeft(2) == nil
}

// largeM: O(rows under the key) locate; the counter is asserted in semi's test.
func largeM() bool {
	e, _ := semi.New(10000)
	for i := range int64(10000) {
		_ = e.AddLeft(i, sp(fmt.Sprintf("k%d", i)))
	}
	return e.AddRight(sp("k9999")) == nil && slices.Equal(e.View(), []int64{9999})
}

func concurrentRead() bool {
	g, _ := api.New(64)
	for i := range int64(10) {
		_ = g.AddLeft(i, sp("k"))
	}
	_ = g.AddRight(sp("k"))
	want := g.View()
	start := make(chan struct{})
	var wg sync.WaitGroup
	var bad atomic.Bool
	for range 16 {
		wg.Go(func() {
			<-start
			for range 50 {
				if !slices.Equal(g.View(), want) || g.SelfCheck() != nil {
					bad.Store(true)
					return
				}
			}
		})
	}
	close(start)
	wg.Wait()
	return !bad.Load()
}

func main() {
	a, b := "x", "x"
	check("key.Match", key.Match(&a, &b) && !key.Match(nil, nil) && !key.Match(&a, nil) && key.Equal(nil, nil))
	check("8-step views (step7 extinguish)", steps())
	noDup, nullOK := semantics()
	check("semi no-dup at ref=2", noDup)
	check("NULL never matches (step8)", nullOK)
	check("4 sentinel errors distinct", errorsDistinct())
	check("rejected ops leave no trace", noTrace())
	check("large-m O(1) locate", largeM())
	g, _ := api.New(8)
	check("api.SelfCheck", g.SelfCheck() == nil)
	check("concurrent read-only consistent", concurrentRead())
	if failed {
		os.Exit(1)
	}
}
