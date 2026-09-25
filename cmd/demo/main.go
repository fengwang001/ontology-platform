// Command demo runs in-process checks for the grouped multi-column exact
// distinct counter. It takes no arguments and performs no network access.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/tup"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK", name)
	} else {
		failed = true
		fmt.Println("FAIL", name)
	}
}

func main() {
	// tup: tuple reference counts flip the distinct count only at 0<->1.
	g := tup.NewGroup()
	a := tup.T{C1: "a", C2: 10}
	check("tup ref-count flips at 0<->1 only",
		g.Add(a) && g.Distinct() == 1 && !g.Add(a) && g.Distinct() == 1 &&
			!g.Remove(a) && g.Distinct() == 1 && g.Remove(a) && g.Distinct() == 0)

	// api: the specification's eight-step sequence, one value per step.
	c := api.New()
	ops := []func() error{
		func() error { return c.Upsert(1, "k", "a", 10) },
		func() error { return c.Upsert(2, "k", "a", 20) },
		func() error { return c.Upsert(3, "k", "b", 10) },
		func() error { return c.Upsert(4, "k", "a", 10) },
		func() error { return c.Upsert(1, "k", "a", 30) },
		func() error { return c.Delete(4) },
		func() error { return c.Upsert(2, "k", "b", 10) },
		func() error { return c.Delete(3) },
	}
	want := []int{1, 2, 3, 3, 4, 3, 2, 2}
	got := make([]int, 8)
	ok8 := true
	for i, op := range ops {
		ok8 = op() == nil && ok8
		got[i] = c.Distinct("k")
		ok8 = ok8 && got[i] == want[i]
	}
	fmt.Printf("OK eight steps=%v final==2:%v\n", got, c.Distinct("k") == 2)
	if !ok8 {
		failed = true
	}

	// api: three mutually distinct sentinel errors; rejected ops leave no trace.
	before := c.Total()
	sentinels := []error{api.ErrRowNotFound, api.ErrRowIDNotPositive, api.ErrEmptyKey}
	rejects := []error{c.Delete(123456), c.Upsert(0, "k", "a", 1), c.Upsert(9, "", "a", 1)}
	okErr := true
	for i, err := range rejects {
		okErr = okErr && errors.Is(err, sentinels[i])
		for j := i + 1; j < len(sentinels); j++ {
			okErr = okErr && !errors.Is(err, sentinels[j])
		}
	}
	check("three distinct errors, rejected ops leave state unchanged", okErr && c.Total() == before)

	// SelfCheck includes the constant tuple-inspection bound for m in
	// 100..10000: a single op never inspects more than 2 tuples.
	check("SelfCheck (8 steps, invariants, large-m constant checks)", c.SelfCheck() == nil)

	// Concurrency: N writers add pairwise distinct tuples; concurrent reads
	// of Distinct("k") must be monotonic non-decreasing, ending at exactly N.
	const N = 200
	cn := api.New()
	var writers, readers sync.WaitGroup
	var mono atomic.Bool
	mono.Store(true)
	stop := make(chan struct{})
	writers.Add(N)
	for i := 1; i <= N; i++ {
		go func(i int) { defer writers.Done(); _ = cn.Upsert(i, "k", "c", i) }(i)
	}
	readers.Add(1)
	go func() {
		defer readers.Done()
		prev := 0
		for {
			select {
			case <-stop:
				return
			default:
				d := cn.Distinct("k")
				if d < prev {
					mono.Store(false)
				}
				prev = d
			}
		}
	}()
	writers.Wait()
	close(stop)
	readers.Wait()
	check(fmt.Sprintf("concurrent %d upserts: distinct==N and reads monotonic", N),
		cn.Distinct("k") == N && mono.Load())

	if failed {
		os.Exit(1)
	}
}
