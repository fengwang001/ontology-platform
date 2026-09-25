package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{{"skeleton", true}}
	fail := 0
	for _, c := range checks {
		status := "OK"
		if !c.ok {
			status, fail = "FAIL", fail+1
		}
		fmt.Printf("%s  %s\n", status, c.name)
	}
	fmt.Printf("total: %d/%d OK\n", len(checks)-fail, len(checks))
	if fail > 0 {
		panic("demo failed")
	}
}
