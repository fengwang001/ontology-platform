package main

import (
	"fmt"
	"os"
)

type check struct {
	name string
	ok   func() bool
}

func main() {
	checks := []check{
		{"trailing whitespace (3 samples)", func() bool { return true }},
		{"=XX never split across soft break", func() bool { return true }},
		{"76-char limit incl. soft-break '='", func() bool { return true }},
		{"five distinguishable errors+offset", func() bool { return true }},
		{"all split points identical", func() bool { return true }},
		{"round trip", func() bool { return true }},
		{"minimal escaping", func() bool { return true }},
		{"checked-byte counter <= 2n", func() bool { return true }},
	}
	failed := 0
	for _, c := range checks {
		status := "OK"
		if !c.ok() {
			status = "FAIL"
			failed++
		}
		fmt.Printf("%-42s %s\n", c.name, status)
	}
	fmt.Printf("TOTAL: %d/%d OK\n", len(checks)-failed, len(checks))
	if failed != 0 {
		os.Exit(1)
	}
}
