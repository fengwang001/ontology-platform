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

	fail := 0
	for _, c := range checks {
		tag := "OK"
		if !c.ok {
			tag, fail = "FAIL", fail+1
		}
		fmt.Printf("%s  %s\n", tag, c.name)
	}
	fmt.Printf("total %d/%d\n", len(checks)-fail, len(checks))
	if fail > 0 {
		panic("demo checks failed")
	}
}
