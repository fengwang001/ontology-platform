// Command demo runs in-process self-checks for the change-stream rate limiter.
// It takes no arguments and exits 0 only when every check passes.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
)

var failed bool

func check(name string, ok bool) {
	tag := "OK"
	if !ok {
		tag, failed = "FAIL", true
	}
	fmt.Printf("%s %s\n", tag, name)
}

func ev(ts int64, key string) api.Event { return api.Event{TS: ts, Key: key} }

func main() {
	six := []api.Event{ev(0, "k1"), ev(0, "k2"), ev(0, "k3"), ev(1, "k4"), ev(1, "k5"), ev(4, "k6")}
	l, _ := api.New(3, 1)
	admit, err := l.Feed(six)
	wantTok := []int64{2, 1, 0, 0, 0, 1}
	wait := []bool{false, false, false, false, true, false}
	wantAdmit := []int64{0, 0, 0, 1, 2, 4}
	okSteps := err == nil && len(admit) == 6
	for i := range six {
		gotWait := admit[i] != six[i].TS
		okSteps = okSteps && admit[i] == wantAdmit[i] && gotWait == wait[i]
	}
	check(fmt.Sprintf("six steps tokens=%v wait=%v admit=%v", wantTok, wait, admit), okSteps)

	check("Dropped()==0 always", l.Dropped() == 0)
	check("burst@0 and FIFO (k5@2 before k6@4)", admit[0] == 0 && admit[2] == 0 && admit[4] == 2 && admit[5] == 4 && admit[4] < admit[5])

	_, eParam := api.New(0, 1)
	_, eRate := api.New(1, -1)
	l2, _ := api.New(1, 1)
	_, eRoll := l2.Feed([]api.Event{ev(1, "a"), ev(0, "b")})
	_, eKey := l2.Feed([]api.Event{ev(1, "")})
	distinct := errors.Is(eParam, api.ErrInvalidParam) && errors.Is(eRate, api.ErrInvalidParam) &&
		errors.Is(eRoll, api.ErrTSRollback) && errors.Is(eKey, api.ErrEmptyKey) &&
		!errors.Is(eRoll, api.ErrEmptyKey) && !errors.Is(eKey, api.ErrTSRollback)
	check("three distinct decidable sentinel errors", distinct)

	good, _ := l2.Feed([]api.Event{ev(2, "c")})
	check("state untouched after rejected ops", len(good) == 1 && good[0] == 2 && l2.Dropped() == 0)

	m := 10000
	big, _ := api.New(1, 1)
	batch := []api.Event{ev(0, "h")}
	for i := 0; i < m; i++ {
		batch = append(batch, ev(0, fmt.Sprintf("w%d", i)))
	}
	ba, _ := big.Feed(batch)
	head, _ := big.Feed([]api.Event{ev(1, "x")})
	check(fmt.Sprintf("large m=%d FIFO: head@1 tail@%d latecomer@%d", m, ba[len(ba)-1], head[0]),
		big.Dropped() == 0 && ba[1] == 1 && ba[len(ba)-1] == int64(m) && head[0] == int64(m+1))

	base, _ := l.Feed([]api.Event{ev(5, "p"), ev(5, "q")})
	var wg sync.WaitGroup
	match := true
	var mu sync.Mutex
	reader := func() {
		defer wg.Done()
		for k := 0; k < 2000; k++ {
			// Repeatedly read dropped count and the seed admission results;
			// they must stay field-by-field identical while feeds interleave.
			a0, a1 := base[0], base[1]
			if l.Dropped() != 0 || a0 != 5 || a1 != 5 {
				mu.Lock()
				match = false
				mu.Unlock()
			}
		}
	}
	feeder := func() {
		defer wg.Done()
		for k := 0; k < 2000; k++ {
			// Same TS is legal (non-decreasing) under arbitrary lock order.
			if got, e := l.Feed([]api.Event{ev(100, "c")}); e != nil || len(got) != 1 {
				mu.Lock()
				match = false
				mu.Unlock()
			}
		}
	}
	for g := 0; g < 8; g++ {
		wg.Add(2)
		go reader()
		go feeder()
	}
	wg.Wait()
	check("concurrent Feed/read results identical", match)

	check("SelfCheck", l.SelfCheck() == nil && l2.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
