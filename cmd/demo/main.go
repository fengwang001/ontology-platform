package main

import "fmt"

type check struct {
	name string
	pass bool
}

func main() {
	checks := []check{
		{"canonical-tail", false},
		{"line-breaks", false},
		{"error-kinds-and-offsets", false},
		{"all-split-points", false},
		{"round-trip", false},
		{"output-limit", false},
		{"inspection-counter", false},
	}

	passed := 0
	for _, item := range checks {
		result := "FAIL"
		if item.pass {
			result = "OK"
			passed++
		}
		fmt.Printf("%s %s\n", result, item.name)
	}
	fmt.Printf("TOTAL %d/%d\n", passed, len(checks))
}
