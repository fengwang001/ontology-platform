package main

import "fmt"

type check struct {
	name string
	fn   func() bool
}

var checks []check

func add(name string, fn func() bool) { checks = append(checks, check{name, fn}) }

func main() {
	pass := 0
	for _, c := range checks {
		if c.fn() {
			fmt.Printf("OK   %s\n", c.name)
			pass++
		} else {
			fmt.Printf("FAIL %s\n", c.name)
		}
	}
	fmt.Printf("total %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		panic("demo failed")
	}
}
