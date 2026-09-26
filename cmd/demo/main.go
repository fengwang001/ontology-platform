package main

import "fmt"

type check struct {
	name string
	ok   bool
}

var checks []check

func record(name string, ok bool) { checks = append(checks, check{name, ok}) }

func main() {
	record("skeleton", true)
	fail := 0
	for _, c := range checks {
		s := "OK"
		if !c.ok {
			s, fail = "FAIL", fail+1
		}
		fmt.Printf("%s %s\n", s, c.name)
	}
	fmt.Printf("TOTAL %d/%d\n", len(checks)-fail, len(checks))
}
