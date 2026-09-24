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
	for _, item := range checks {
		status := "FAIL"
		if item.ok {
			status = "OK"
			pass++
		}
		fmt.Printf("%s %s\n", status, item.name)
	}
	fmt.Printf("total %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		panic("demo failed")
	}
}
