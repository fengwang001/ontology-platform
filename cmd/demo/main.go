package main

import (
	"fmt"
	"os"
)

type check struct {
	name string
	ok   bool
}

func main() {
	var results []check
	results = append(results, check{"skeleton", true})

	fail := 0
	for _, r := range results {
		status := "OK"
		if !r.ok {
			status, fail = "FAIL", fail+1
		}
		fmt.Printf("%-28s %s\n", r.name, status)
	}
	fmt.Printf("total: %d check(s), %d fail\n", len(results), fail)
	if fail > 0 {
		os.Exit(1)
	}
}
