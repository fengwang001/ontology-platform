package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"skeleton", true},
	}

	passed := 0
	for _, item := range checks {
		status := "FAIL"
		if item.ok {
			status = "OK"
			passed++
		}
		fmt.Printf("%s %s\n", status, item.name)
	}
	fmt.Printf("total %d/%d\n", passed, len(checks))
	if passed != len(checks) {
		panic("demo checks failed")
	}
}
