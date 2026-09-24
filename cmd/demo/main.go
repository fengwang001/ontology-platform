package main

import "fmt"

type check struct {
	name string
	ok   bool
}

var results []check

func record(name string, ok bool) { results = append(results, check{name, ok}) }

func main() {
	record("skeleton", true)

	pass := true
	for _, r := range results {
		status := "OK"
		if !r.ok {
			status, pass = "FAIL", false
		}
		fmt.Printf("%s %s\n", status, r.name)
	}
	fmt.Printf("TOTAL %d/%d\n", countPass(), len(results))
	if !pass {
		panic("FAIL")
	}
}

func countPass() int {
	n := 0
	for _, r := range results {
		if r.ok {
			n++
		}
	}
	return n
}
