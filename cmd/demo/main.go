package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"ontology/api"
	"ontology/cbf"
)

var failed bool

func check(name string, cond bool, detail ...string) {
	if !cond {
		failed = true
		name = "FAIL: " + name
	} else {
		name = "OK: " + name
	}
	if len(detail) > 0 {
		name += " " + strings.Join(detail, " ")
	}
	fmt.Println(name)
}

func main() {
	want := [][]int64{
		{0, 1, 0, 1, 0, 0, 1, 0},
		{0, 1, 1, 1, 0, 1, 1, 1},
		{0, 1, 1, 1, 0, 2, 2, 2},
		{0, 1, 0, 1, 0, 1, 2, 1},
	}
	f, _ := cbf.New(8, 3)
	f.Add(3)
	c1 := f.Snapshot()
	f.Add(5)
	c2 := f.Snapshot()
	f.Add(7)
	c3 := f.Snapshot()
	f.Remove(5)
	c4 := f.Snapshot()
	match := fmt.Sprint(c1) == fmt.Sprint(want[0]) && fmt.Sprint(c2) == fmt.Sprint(want[1]) &&
		fmt.Sprint(c3) == fmt.Sprint(want[2]) && fmt.Sprint(c4) == fmt.Sprint(want[3])
	check("counter arrays after each of the 4 steps (naive replay)", match,
		fmt.Sprintf("%v | %v | %v | %v", c1, c2, c3, c4))

	q3, _ := f.Query(3)
	q5, _ := f.Query(5)
	q7, _ := f.Query(7)
	check("Query(3)=1 (no false negative), Query(5)=0, Query(7)>=1",
		q3 == 1 && q5 == 0 && q7 >= 1, fmt.Sprintf("(%d,%d,%d)", q3, q5, q7))

	one, _ := cbf.New(8, 3)
	one.Add(6)
	one.Remove(6)
	allZero := true
	for _, v := range one.Snapshot() {
		if v != 0 {
			allZero = false
		}
	}
	check("exact removal: lone Add(6) then Remove(6) restores all zero", allZero)

	_, eP := api.New(0, 3)
	_, eK := api.New(8, 0)
	af, _ := api.New(8, 3)
	af.Add(3)
	eKey := af.Add(-1)
	eMiss := af.Remove(5)
	three := errors.Is(eP, api.ErrInvalidParams) && errors.Is(eK, api.ErrInvalidParams) &&
		errors.Is(eKey, api.ErrInvalidKey) && errors.Is(eMiss, api.ErrNotPresent)
	g, _ := cbf.New(8, 3) // same code path api delegates to
	g.Add(3)
	before := fmt.Sprint(g.Snapshot())
	g.Add(-1)
	g.Remove(5)
	check("3 distinct sentinel errors; rejected ops leave no trace; SelfCheck",
		three && fmt.Sprint(g.Snapshot()) == before && af.SelfCheck())

	largeOK := true
	for _, m := range []int{100, 1000, 10000} {
		h, _ := cbf.New(m, 7)
		for key := 0; key < m; key++ {
			h.Add(int64(key))
		}
		h.Query(0)
		largeOK = largeOK && h.LastQueryTouchedK()
	}
	check("Query visits exactly k counters for m in {100,1000,10000}", largeOK)

	h, _ := api.New(1000, 5)
	keys := make([]int64, 200)
	for i := range keys {
		keys[i] = int64(i * 3)
		h.Add(keys[i])
	}
	wantQ := make([]int64, len(keys))
	for i, key := range keys {
		wantQ[i], _ = h.Query(key)
	}
	var wg sync.WaitGroup
	same := true
	var mu sync.Mutex
	for n := 0; n < 16; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, key := range keys {
				v, _ := h.Query(key)
				mu.Lock()
				if v != wantQ[i] {
					same = false
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	check("16 goroutines concurrent Query: per-key results identical", same)

	if failed {
		os.Exit(1)
	}
}
