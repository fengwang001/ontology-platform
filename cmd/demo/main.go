package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"trailing-space-newline", false},
		{"trailing-tab-eof", false},
		{"interior-space", false},
		{"atomic-escaped-token", false},
		{"76-column-limit", false},
		{"five-error-kinds-and-offsets", false},
		{"split-independence", false},
		{"round-trip", false},
		{"minimal-escape", false},
		{"input-byte-counter", false},
	}
	pass := 0
	for _, item := range checks {
		status := "FAIL"
		if item.ok {
			status = "OK"
			pass++
		}
		fmt.Printf("%s %s\n", status, item.name)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
}
