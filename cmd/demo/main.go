package main

import (
	"errors"
	"fmt"
	"time"

	"ontology/interval"
)

type check struct {
	name string
	ok   bool
}

func main() {
	var results []check
	tm := func(n int64) time.Time { return time.Unix(n, 0).UTC() }

	iv, err := interval.New(tm(10), tm(20))
	results = append(results, check{"interval start hits / end misses",
		err == nil && iv.Contains(tm(10)) && !iv.Contains(tm(20))})
	_, errEmpty := interval.New(tm(10), tm(10))
	results = append(results, check{"empty interval rejected",
		errors.Is(errEmpty, interval.ErrEmptyInterval)})

	pass := 0
	for _, r := range results {
		if r.ok {
			pass++
			fmt.Println("OK  " + r.name)
		} else {
			fmt.Println("FAIL " + r.name)
		}
	}
	fmt.Printf("TOTAL: %d/%d passed\n", pass, len(results))
	if pass != len(results) {
		panic("demo checks failed")
	}
}
