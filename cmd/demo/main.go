package main

import (
	"fmt"
	"os"
)

type check struct {
	name string
	fn   func() error
}

var checks []check

func main() {
	checks = append(checks, check{name: "skeleton", fn: func() error { return nil }})
	failed := 0
	for _, c := range checks {
		if err := c.fn(); err != nil {
			failed++
			fmt.Printf("FAIL %s: %v\n", c.name, err)
			continue
		}
		fmt.Printf("OK %s\n", c.name)
	}
	if failed > 0 {
		fmt.Printf("TOTAL %d/%d failed\n", failed, len(checks))
		os.Exit(1)
}
	fmt.Printf("TOTAL all %d checks passed\n", len(checks))
}
