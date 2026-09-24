package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"canonical samples", false},
		{"newline positions", false},
		{"five error classes and offsets", false},
		{"every split point", false},
		{"round trips", false},
		{"output limit", false},
		{"byte examination counter", false},
	}

	passed := 0
	for _, item := range checks {
		label := "FAIL"
		if item.ok {
			label = "OK"
			passed++
		}
		fmt.Printf("%s %s\n", label, item.name)
	}
	fmt.Printf("TOTAL %d/%d\n", passed, len(checks))
}
