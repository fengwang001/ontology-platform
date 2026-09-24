package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{{"skeleton", true}}
	pass := 0
	for _, c := range checks {
		s := "OK"
		if !c.ok {
			s = "FAIL"
		} else {
			pass++
		}
		fmt.Printf("%s %s\n", s, c.name)
	}
	fmt.Printf("total %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		panic("demo failed")
	}
}
