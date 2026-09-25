package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"time"

	"ontology/aln"
	"ontology/api"
)

var failures int

func check(name string, ok bool) {
	if !ok {
		failures++
	}
	status := "OK: "
	if !ok {
		status = "FAIL: "
	}
	fmt.Println(status + name)
}

func main() {
	plan := [][]api.Event{
		{{Key: "k", TS: 10, V: 1}, {Key: "k", TS: 5, V: 2}},
		{{Key: "k", TS: 20, V: 3}, {Key: "k", TS: 12, V: 4}},
		{{Key: "k", TS: 8, V: 5}, {Key: "k", TS: 12, V: 6}},
		{{Key: "k", TS: 25, V: 7}, {Key: "k", TS: 25, V: 9}},
	}
	v, _ := api.New(8)
	var steps []int
	for _, b := range plan {
		v.BeginBatch()
		for _, e := range b {
			v.Feed(e)
			val, _ := v.Value("k")
			steps = append(steps, val)
		}
	}
	v.Flush()
	at := v.AlignedTimes()
	check(fmt.Sprintf("per-step values %v", steps), reflect.DeepEqual(steps, []int{1, 1, 3, 3, 3, 3, 7, 9}))
	check(fmt.Sprintf("aligned A1/A2/A3 = %d/%d/%d", at[1], at[2], at[3]), reflect.DeepEqual(at, []int64{5, 12, 12, 25}))
	check(fmt.Sprintf("dropped = %d", v.Dropped()), v.Dropped() == 1)

	_, e1 := api.New(0)
	idle, _ := api.New(2)
	e2 := idle.Feed(api.Event{Key: "k", TS: 1})
	z, _ := api.New(1)
	z.BeginBatch()
	e3 := z.Feed(api.Event{Key: "", TS: 1})
	z.Feed(api.Event{Key: "k", TS: 5, V: 5})
	e4 := z.Feed(api.Event{Key: "k", TS: 6, V: 6})
	distinct := e1 != e2 && e1 != e3 && e1 != e4 && e2 != e3 && e2 != e4 && e3 != e4
	check("4 distinct decidable errors", distinct &&
		errors.Is(e1, api.ErrInvalidParam) && errors.Is(e2, api.ErrNoOpenBatch) &&
		errors.Is(e3, api.ErrInvalidEvent) && errors.Is(e4, api.ErrBatchTooLarge))
	got, _ := z.Value("k")
	check("rejections leave no trace", got == 5 && z.Dropped() == 0 && len(z.AlignedTimes()) == 0)

	errs := make(chan string, 8)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				val, _ := v.Value("k")
				if val != 9 || v.Dropped() != 1 || !reflect.DeepEqual(v.AlignedTimes(), at) {
					errs <- "mismatch"
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	_, bad := <-errs
	check("8 goroutines x 100 reads identical", !bad)
	check("SelfCheck", v.SelfCheck() == nil)

	// aln: large-m strictly decreasing TS. Incremental running min is
	// O(1)/event (events are not stored); a full-batch O(m^2) rescan
	// cannot finish in the budget. This demonstrates cmps==m behaviour
	// without ever reading the unexported counter through a public API.
	const m = 2_000_000
	a := aln.New()
	a.Begin()
	start := time.Now()
	for i := m; i > 0; i-- {
		a.Accept(int64(i))
	}
	elapsed := time.Since(start)
	a.Close()
	check(fmt.Sprintf("running min incremental at m=%d (%v, aligned=%d)", m, elapsed.Round(time.Millisecond), a.Aligned()[0]),
		elapsed < 200*time.Millisecond && a.Aligned()[0] == 1)

	if failures > 0 {
		os.Exit(1)
	}
}
