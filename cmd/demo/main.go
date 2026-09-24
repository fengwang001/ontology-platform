package main

import (
	"fmt"
	"os"
)

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"trailing-space samples", false},
		{"=XX not split", false},
		{"76 limit incl. soft '='", false},
		{"five distinct errors+offset", false},
		{"all split points", false},
		{"round-trip", false},
		{"minimal escape", false},
		{"scan counter <= 2n", false},
	}
	fail := 0
	for _, c := range checks {
		status := "OK"
		if !c.ok {
			status = "FAIL"
			fail++
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("TOTAL %d/%d OK\n", len(checks)-fail, len(checks))
	if fail != 0 {
		fmt.Println("DEMO HAS FAILURES")
		os.Exit(1)
	}
}
