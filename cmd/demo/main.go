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
		{"control character offset", pending},
		{"unknown escape offset", pending},
		{"short unicode offset", pending},
		{"missing closing quote offset", pending},
		{"trailing byte offset", pending},
		{"all surrogate pair cases", pending},
		{"invalid UTF-8 both directions", pending},
		{"minimal escaping", pending},
		{"round trip", pending},
		{"all split points agree", pending},
		{"inspection counter", pending},
	}

	passed := 0
	for _, item := range checks {
		if item.ok() {
			passed++
			fmt.Printf("OK   %s\n", item.name)
			continue
		}
		fmt.Printf("FAIL %s\n", item.name)
	}
	fmt.Printf("TOTAL %d/%d\n", passed, len(checks))
	if passed != len(checks) {
		os.Exit(1)
	}
}

func pending() bool { return false }
