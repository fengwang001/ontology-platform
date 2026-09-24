package main

import "fmt"

type check struct {
	name string
	fn   func() error
}

var checks []check

func main() {
	checks = append(checks, check{name: "skeleton", fn: func() error { return nil }})
	fail := 0
	for _, c := range checks {
		if err := c.fn(); err != nil {
			fmt.Printf("FAIL %s: %v\n", c.name, err)
			fail++
			continue
		}
		fmt.Printf("OK %s\n", c.name)
	}
	if fail > 0 {
		fmt.Printf("TOTAL %d FAIL\n", fail)
		return
	}
	fmt.Println("TOTAL ALL OK")
}
