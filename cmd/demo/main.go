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
	pass := 0
	for _, c := range checks {
		if c.ok {
			pass++
			fmt.Println("OK  ", c.name)
		} else {
			fmt.Println("FAIL", c.name)
		}
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		panic("demo failed")
	}
}
