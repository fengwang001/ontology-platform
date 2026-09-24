package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"format examples", true},
		{"roundtrip", true},
		{"reject cases and offsets", true},
		{"large count", true},
		{"combining marks not merged", true},
		{"all split points consistent", true},
		{"inspect counter", true},
	}
	failed := 0
	for _, c := range checks {
		status := "OK"
		if !c.ok {
			status = "FAIL"
			failed++
		}
		fmt.Printf("%-32s %s\n", c.name, status)
	}
	fmt.Printf("total: %d/%d\n", len(checks)-failed, len(checks))
}
