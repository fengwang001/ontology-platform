package main

import "fmt"

type check struct {
	name string
	fn   func() error
}

func main() {
	checks := []check{}

	pass, fail := 0, 0
	for _, c := range checks {
		if err := c.fn(); err != nil {
			fail++
			fmt.Printf("FAIL %-22s %v\n", c.name, err)
		} else {
			pass++
			fmt.Printf("OK   %s\n", c.name)
		}
	}
	fmt.Printf("TOTAL pass=%d fail=%d\n", pass, fail)
	if fail != 0 {
		panic("checks failed")
	}
}
