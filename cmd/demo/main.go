// Command demo exercises the bitemporal AsOf query engine end to end.
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/interval"
	"ontology/record"
)

var passed, failed int

func judge(name string, ok bool) {
	if ok {
		passed++
		fmt.Printf("OK   %s\n", name)
		return
	}
	failed++
	fmt.Printf("FAIL %s\n", name)
}

func main() {
	iv, err := interval.New(10, 20)
	judge("区间起点命中、终点不命中", err == nil && iv.Contains(10) && !iv.Contains(20))

	_, err = interval.New(5, 5)
	judge("空区间被拒且可判定", errors.Is(err, interval.ErrEmpty))

	_, err = record.New("k", 1, interval.Forever(0), interval.Interval{Start: 2, End: 2})
	judge("空事务区间的记录被拒", errors.Is(err, interval.ErrEmpty))

	fmt.Printf("TOTAL %d/%d OK\n", passed, passed+failed)
	if failed > 0 {
		os.Exit(1)
	}
}
