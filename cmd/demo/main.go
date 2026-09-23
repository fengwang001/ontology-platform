package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{name: "skeleton", ok: true},
	}

	failed := 0
	for _, c := range checks {
		status := "OK"
		if !c.ok {
			status = "FAIL"
			failed++
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("TOTAL %d/%d\n", len(checks)-failed, len(checks))
	if failed != 0 {
		fmt.Println("FAILURES PRESENT")
	}
}
