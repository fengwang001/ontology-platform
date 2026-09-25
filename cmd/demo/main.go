package main

import "fmt"

type check struct {
	name   string
	ok     bool
	detail string
}

func main() {
	checks := []check{skeleton()}
	pass := 0
	for _, c := range checks {
		s := "OK"
		if !c.ok {
			s = "FAIL " + c.detail
		} else {
			pass++
		}
		fmt.Printf("%-28s %s\n", c.name, s)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		panic("checks failed")
	}
}

func skeleton() check { return check{"skeleton", true, ""} }
