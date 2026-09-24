package main

import (
	"fmt"
	"os"
)

type check struct {
	name string
	ok   bool
}

var results []check

func record(name string, ok bool) {
	results = append(results, check{name, ok})
}

func main() {
	// 判定在实现各包后逐条补充。
	record("placeholder", true)

	failed := 0
	for _, r := range results {
		status := "OK"
		if !r.ok {
			status = "FAIL"
			failed++
		}
		fmt.Printf("%s %s\n", status, r.name)
	}
	fmt.Printf("TOTAL %d/%d\n", len(results)-failed, len(results))
	if failed != 0 {
		os.Exit(1)
	}
}
