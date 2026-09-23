package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"trailing-space-before-newline", false},
		{"space-inside-line", false},
		{"trailing-space-at-eof", false},
		{"escape-not-split", false},
		{"line-limit-76", false},
		{"five-error-kinds-with-offset", false},
		{"all-split-points", false},
		{"roundtrip", false},
		{"minimal-escape", false},
		{"byte-inspection-counter", false},
	}
	pass := 0
	for _, c := range checks {
		status := "FAIL"
		if c.ok {
			status, pass = "OK", pass+1
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
}
