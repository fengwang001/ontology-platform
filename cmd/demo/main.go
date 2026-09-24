package main

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"

	"ontology/asof"
	"ontology/interval"
	"ontology/record"
	"ontology/store"
)

var failed int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed++
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	n := 0
	iv, err := interval.New(2020, 2021)
	n++
	check("interval start hit, end miss", err == nil && iv.Contains(2020) && !iv.Contains(2021))
	_, err = interval.New(2021, 2021)
	n++
	check("empty interval rejected", errors.Is(err, interval.ErrEmpty))
	r1 := record.Record{Key: "k", Value: 1, Valid: iv, Tx: interval.Interval{Start: 0, End: 5}}
	r2 := record.Record{Key: "k", Value: 2, Valid: iv, Tx: interval.Interval{Start: 5, End: interval.Forever}}
	n++
	check("record covers point, rectangles disjoint", r1.Covers(2020, 4) && !r1.Covers(2020, 5) && record.Disjoint(r1, r2))

	s := store.New()
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("k%d", rng.Intn(20))
		start := int64(rng.Intn(50))
		iv, _ := interval.New(start, start+1+int64(rng.Intn(5)))
		switch rng.Intn(3) {
		case 0:
			_, _ = s.Put(key, int64(i), iv)
		case 1:
			_, _ = s.Correct(key, int64(i), iv)
		default:
			_, _ = s.Delete(key, start)
		}
	}
	n++
	check("rectangle invariant holds after 1000 random ops", s.CheckInvariant() == nil)

	cs := store.New()
	civ, _ := interval.New(2020, 2021)
	if _, err := cs.Put("salary", 100, civ); err != nil {
		check("concurrent setup put", false)
	}
	var wg sync.WaitGroup
	for g := 0; g < 100; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_, _ = cs.Correct("salary", int64(g*100+i), civ)
			}
		}(g)
	}
	wg.Wait()
	n++
	check("invariant holds after concurrent corrections", cs.CheckInvariant() == nil)

	bs := store.New()
	sal, _ := interval.New(2020, 2021)
	tx1, _ := bs.Put("alice", 100, sal)
	tx2, _ := bs.Correct("alice", 120, sal)
	v1, ok1 := asof.Query(bs, "alice", 2020, tx1)
	v2, ok2 := asof.Query(bs, "alice", 2020, tx2)
	n++
	check("same valid time, two tx times differ", ok1 && ok2 && v1.Value == 100 && v2.Value == 120)

	ds := store.New()
	span, _ := interval.New(2020, 2030)
	_, _ = ds.Put("bob", 77, span)
	delTx, _ := ds.Delete("bob", 2025)
	hist, hok := asof.Query(ds, "bob", 2020, delTx)
	_, gok := asof.Query(ds, "bob", 2026, delTx)
	n++
	check("delete: history visible, current absent", hok && hist.Value == 77 && !gok)

	zs := store.New()
	ztx, _ := zs.Put("zero", 0, span)
	zv, zok := asof.Query(zs, "zero", 2020, ztx)
	_, nok := asof.Query(zs, "zero", 2031, ztx)
	n++
	check("zero value distinguishable from absent", zok && zv.Value == 0 && !nok)

	ps := store.New()
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("p%d", i)
		_, _ = ps.Put(key, 0, sal)
		for v := 1; v < 10; v++ {
			_, _ = ps.Correct(key, int64(v), sal)
		}
	}
	_, pok := asof.Query(ps, "p999", 2020, 10000)
	n++
	check("point query checks within bound", pok && asof.Checked() <= 4*10)

	_, early := asof.Query(bs, "alice", 2020, tx1-1)
	n++
	check("tx time too early returns absent", !early)
	fmt.Printf("TOTAL %d checks, %d failed\n", n, failed)
}
