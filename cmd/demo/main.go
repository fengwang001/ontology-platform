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
		{"trailing whitespace (3 samples)", false},
		{"=XX never split by soft break", false},
		{"76-col limit incl. soft '='", false},
		{"5 distinct errors with offsets", false},
		{"all split points identical", false},
		{"round trip", false},
		{"minimal escaping", false},
		{"inspection counter <= 2*len", false},
	}
	fails := 0
	for _, c := range checks {
		status := "OK"
		if !c.ok {
			status, fails = "FAIL", fails+1
		}
		fmt.Printf("%-42s %s\n", c.name, status)
	}
	fmt.Printf("TOTAL: %d/%d OK\n", len(checks)-fails, len(checks))
	if fails > 0 {
		os.Exit(1)
	}
}
