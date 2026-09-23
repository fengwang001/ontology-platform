package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"strict tail", false},
		{"newline placement", false},
		{"error classes and offsets", false},
		{"all split points", false},
		{"round trip", false},
		{"output limit", false},
		{"inspection counter", false},
	}

	passed := 0
	for _, item := range checks {
		status := "FAIL"
		if item.ok {
			status = "OK"
			passed++
		}
		fmt.Printf("%s: %s\n", status, item.name)
	}
	fmt.Printf("total: %d/%d\n", passed, len(checks))
}
