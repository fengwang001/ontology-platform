// Command demo exercises the reassembler's nine semantics and prints
// one OK/FAIL verdict per check plus a summary line.
package main

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"ontology/reasm"
)

var now = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func clock() time.Time { return now }

var passed, failed int

func check(name string, ok bool, detail string) {
	if ok {
		passed++
		fmt.Printf("OK   %s %s\n", name, detail)
	} else {
		failed++
		fmt.Printf("FAIL %s %s\n", name, detail)
	}
}

func main() {
	outOfOrder()
	duplicate()
	conflict()
	adjacentMerge()
	outOfRange()
	eviction()
	budgetLimit()
	deliveredQueryZero()
	concurrentOnce()
	fmt.Printf("TOTAL %d passed, %d failed\n", passed, failed)
}

func outOfOrder() {
	r := reasm.New(clock, time.Minute, 1<<20)
	full := []byte("reassemble-out-of-order!")
	deliveries := 0
	var got []byte
	for _, off := range []int{16, 0, 8} {
		m, done, _ := r.Submit("m", off, full[off:off+8], len(full))
		if done {
			deliveries++
			got = m
		}
	}
	check("out-of-order", deliveries == 1 && string(got) == string(full) && r.Used() == 0,
		fmt.Sprintf("deliveries=%d bytes=%q", deliveries, got))
}

func duplicate() {
	r := reasm.New(clock, time.Minute, 100)
	_, _, _ = r.Submit("m", 0, []byte("abcd"), 10)
	used := r.Used()
	_, done, err := r.Submit("m", 0, []byte("abcd"), 10)
	check("duplicate-idempotent", err == nil && !done && r.Used() == used,
		fmt.Sprintf("used=%d err=%v", r.Used(), err))
}

func conflict() {
	r := reasm.New(clock, time.Minute, 100)
	_, _, _ = r.Submit("m", 0, []byte("abcde"), 10)
	_, _, err := r.Submit("m", 3, []byte("dZZ"), 10)
	var ce *reasm.ConflictError
	ok := errors.As(err, &ce) && ce.Start == 4 && ce.End == 5 &&
		r.Query("m").Received == 5 && r.Used() == 5
	check("conflict-detectable", ok, fmt.Sprintf("err=%v", err))
}

func adjacentMerge() {
	r := reasm.New(clock, time.Minute, 100)
	_, _, _ = r.Submit("m", 0, []byte("aaaaa"), 12)
	_, _, _ = r.Submit("m", 5, []byte("bbbbb"), 12)
	ivs := r.Intervals("m")
	ok := len(ivs) == 1 && ivs[0].Start == 0 && ivs[0].End == 10
	check("adjacent-merge", ok, fmt.Sprintf("intervals=%v", ivs))
}

func outOfRange() {
	r := reasm.New(clock, time.Minute, 100)
	_, _, err := r.Submit("m", 8, []byte("xyz"), 10)
	check("out-of-range-rejected", errors.Is(err, reasm.ErrOutOfRange),
		fmt.Sprintf("err=%v", err))
}

func eviction() {
	r := reasm.New(clock, time.Minute, 100)
	_, _, _ = r.Submit("m", 0, []byte("abcde"), 10)
	now = now.Add(time.Minute) // exactly at expiry: evicted
	info := r.Query("m")
	_, done, _ := r.Submit("m", 5, []byte("fghij"), 10) // restarts fresh
	ok := info == (reasm.Info{}) && r.Used() == 5 && !done
	check("expiry-eviction", ok, fmt.Sprintf("used=%d info=%+v", r.Used(), info))
}

func budgetLimit() {
	r := reasm.New(clock, time.Minute, 10)
	_, _, _ = r.Submit("m", 0, []byte("abcdef"), 12)
	_, _, err := r.Submit("m", 6, []byte("ghijk"), 12)
	ok := errors.Is(err, reasm.ErrBudgetExceeded) &&
		r.Used() == 6 && r.Query("m").Received == 6
	check("budget-hard-limit", ok, fmt.Sprintf("err=%v used=%d", err, r.Used()))
}

func deliveredQueryZero() {
	r := reasm.New(clock, time.Minute, 100)
	_, done, _ := r.Submit("m", 0, []byte("ab"), 2)
	info := r.Query("m")
	check("delivered-query-zero", done && info == (reasm.Info{}) && r.Used() == 0,
		fmt.Sprintf("info=%+v", info))
}

func concurrentOnce() {
	r := reasm.New(clock, time.Hour, 1<<20)
	full := make([]byte, 2000)
	for i := range full {
		full[i] = byte(i)
	}
	var deliveries atomic.Int64
	var wg sync.WaitGroup
	for off := 0; off < len(full); off += 100 {
		wg.Add(1)
		go func(off int) {
			defer wg.Done()
			_, done, err := r.Submit("race", off, full[off:off+100], len(full))
			if err == nil && done {
				deliveries.Add(1)
			}
		}(off)
	}
	wg.Wait()
	check("concurrent-single-deliverer", deliveries.Load() == 1 && r.Used() == 0,
		fmt.Sprintf("deliveries=%d", deliveries.Load()))
}
