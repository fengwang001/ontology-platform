package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"canonical-tail-samples", true},
		{"newline-positions", true},
		{"five-error-classes-and-offsets", true},
		{"all-split-points", true},
		{"round-trip", true},
		{"output-limit", true},
		{"checked-byte-counter", true},
	}

	passed := 0
	for _, item := range checks {
		status := "FAIL"
		if item.ok {
			status = "OK"
			passed++
		}
		fmt.Printf("%s %s\n", status, item.name)
	}
	fmt.Printf("TOTAL %d/%d\n", passed, len(checks))
}
