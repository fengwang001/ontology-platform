package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"trailing-space-three-cases", false},
		{"escape-token-not-split", false},
		{"line-limit-76-with-soft-equals", false},
		{"five-distinct-errors-and-offsets", false},
		{"all-split-points-consistent", false},
		{"round-trip", false},
		{"minimal-escaping", false},
		{"inspection-counter", false},
	}
	passed := 0
	for _, c := range checks {
		status := "FAIL"
		if c.ok {
			status, passed = "OK", passed+1
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("TOTAL %d/%d\n", passed, len(checks))
}
