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
	fails := 0
	for _, c := range checks {
		mark := "OK"
		if !c.ok {
			mark = "FAIL"
			fails++
		}
		fmt.Printf("%s  %s\n", mark, c.name)
	}
	fmt.Printf("TOTAL %d/%d\n", len(checks)-fails, len(checks))
	if fails != 0 {
		fmt.Println("FAILURES PRESENT")
	}
}
