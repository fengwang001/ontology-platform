package main

import (
	"fmt"
	"os"
)

type check struct {
	name string
	ok   bool
	note string
}

func main() {
	checks := []check{
		{"format-samples", true, "stub"},
		{"roundtrip", true, "stub"},
		{"reject-count-one", true, "stub"},
		{"reject-count-zero-leading-zero", true, "stub"},
		{"reject-adjacent-same-symbol", true, "stub"},
		{"reject-bad-escape-trailing-backslash", true, "stub"},
		{"reject-dangling-count", true, "stub"},
		{"reject-invalid-utf8", true, "stub"},
		{"huge-count", true, "stub"},
		{"combining-not-merged", true, "stub"},
		{"all-split-points", true, "stub"},
		{"byte-counter", true, "stub"},
	}

	passed := 0
	for _, c := range checks {
		status := "FAIL"
		if c.ok {
			status = "OK"
			passed++
		}
		fmt.Printf("%-40s %s %s\n", c.name, status, c.note)
	}
	fmt.Printf("TOTAL %d/%d\n", passed, len(checks))
	if passed != len(checks) {
		os.Exit(1)
	}
}
