package main

import (
	"fmt"
	"os"
)

type check struct {
	name string
	ok   bool
}

var checks []check

func record(name string, ok bool) { checks = append(checks, check{name, ok}) }

func main() {
	record("placeholder", true)

	pass := 0
	for _, c := range checks {
		status := "OK"
		if !c.ok {
			status = "FAIL"
		} else {
			pass++
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		os.Exit(1)
	}
}
