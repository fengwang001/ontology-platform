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
		fmt.Printf("%-28s %s\n", c.name, status)
	}
	fmt.Printf("total %d, fail %d\n", len(checks), fail)
	if fail > 0 {
		panic("demo failed")
	}
}
