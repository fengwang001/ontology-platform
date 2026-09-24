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
	fail := 0
	for _, c := range checks {
		status := "OK"
		if !c.ok {
			status = "FAIL"
			fail++
		}
		fmt.Printf("%-28s %s\n", c.name, status)
	}
	fmt.Printf("total: %d/%d OK\n", len(checks)-fail, len(checks))
	if fail > 0 {
		fmt.Println("DEMO FAILED")
		return
	}
}
