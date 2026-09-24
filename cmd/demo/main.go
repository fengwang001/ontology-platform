package main

import "fmt"

type result struct {
	name string
	ok   bool
}

func main() {
	results := []result{
		{"strict errors and offsets", false},
		{"surrogate rules", false},
		{"invalid UTF-8 in both directions", false},
		{"minimal escaping", false},
		{"round trip", false},
		{"all stream split points", false},
		{"byte check counter", false},
	}

	passed := 0
	for _, result := range results {
		status := "FAIL"
		if result.ok {
			status = "OK"
			passed++
		}
		fmt.Printf("%s: %s\n", status, result.name)
	}
	fmt.Printf("TOTAL: %d/%d\n", passed, len(results))
	if passed != len(results) {
		panic("demo checks failed")
	}
}
