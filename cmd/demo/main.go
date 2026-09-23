package main

import "fmt"

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
		fmt.Printf("%s  %s\n", status, r.name)
	}
	fmt.Printf("TOTAL %d/%d\n", len(results)-fail, len(results))
	if fail > 0 {
		panic("demo failed")
	}
}
