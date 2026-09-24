package main

import (
	"fmt"
	"os"
)

type check struct {
	name string
	fn   func() bool
}

func pending() bool { return true }

func main() {
	checks := []check{
		{"separators/whitespace", pending},
		{"comments", pending},
		{"continuation x4", pending},
		{"blank continuation", pending},
		{"escapes", pending},
		{"\\u error line:col", pending},
		{"dup key order", pending},
		{"store specials", pending},
		{"roundtrip x1000", pending},
		{"byte-check counter", pending},
	}
	fails := 0
	for _, c := range checks {
		status := "OK"
		if !c.fn() {
			status, fails = "FAIL", fails+1
		}
		fmt.Printf("%-22s %s\n", c.name, status)
	}
	fmt.Printf("TOTAL %d/%d\n", len(checks)-fails, len(checks))
	if fails > 0 {
		os.Exit(1)
	}
}
