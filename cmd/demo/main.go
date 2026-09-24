package main

import "fmt"

type check struct {
	name string
	ok   bool
}

var checks []check

func add(name string, ok bool) { checks = append(checks, check{name, ok}) }

func main() {
	add("skeleton", true)

	pass := 0
	for _, c := range checks {
		mark := "OK"
		if !c.ok {
			mark = "FAIL"
		} else {
			pass++
		}
		fmt.Printf("%-28s %s\n", c.name, mark)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		panic("checks failed")
	}
}
