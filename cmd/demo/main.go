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
		status := "FAIL"
		if c.ok {
			status, pass = "OK", pass+1
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		panic("demo failed")
	}
}
