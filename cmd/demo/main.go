package main

import (
	"fmt"
	"os"
)

type check struct {
	name   string
	ok     bool
	detail string
}

func main() {
	checks := []check{
		{"skeleton", true, ""},
	}
	fail := 0
	for _, c := range checks {
		status := "OK"
		if !c.ok {
			status = "FAIL"
			fail++
		}
		fmt.Printf("%s %s %s\n", status, c.name, c.detail)
	}
	fmt.Printf("total: %d checks, %d failed\n", len(checks), fail)
	if fail > 0 {
		os.Exit(1)
	}
}
