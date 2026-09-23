package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"strict-tail", false},
		{"newline-position", false},
		{"error-kinds-offset", false},
		{"all-splits", false},
		{"roundtrip", false},
		{"output-limit", false},
		{"byte-counter", false},
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
	fmt.Printf("total %d/%d\n", passed, len(checks))
}
