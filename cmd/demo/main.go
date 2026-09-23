package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	var results []check

	results = append(results, check{"skeleton", true})

	pass := 0
	for _, r := range results {
		if r.ok {
			pass++
			fmt.Printf("OK   %s\n", r.name)
		} else {
			fmt.Printf("FAIL %s\n", r.name)
		}
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(results))
	if pass != len(results) {
		panic("demo checks failed")
	}
}
