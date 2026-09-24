package main

import "fmt"

type check struct {
	name string
	pass bool
}

func main() {
	checks := []check{
		{"skeleton", true},
	}
	n := 0
	for _, c := range checks {
		r := "FAIL"
		if c.pass {
			r, n = "OK", n+1
		}
		fmt.Printf("%s %s\n", r, c.name)
	}
	fmt.Printf("TOTAL %d/%d\n", n, len(checks))
	if n != len(checks) {
		panic("demo failed")
	}
}
