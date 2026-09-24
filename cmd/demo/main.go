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
		{"skeleton", true},
	}
	failed := 0
	for _, c := range checks {
		status := "OK"
		if !c.ok {
			status = "FAIL"
			failed++
		}
		fmt.Printf("%s  %s\n", status, c.name)
	}
	fmt.Printf("total: %d, failed: %d\n", len(checks), failed)
	if failed != 0 {
		os.Exit(1)
	}
}
