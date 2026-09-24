package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{{name: "skeleton", ok: true}}
	fail := 0
	for _, c := range checks {
		if c.ok {
			fmt.Println("OK  " + c.name)
		} else {
			fmt.Println("FAIL " + c.name)
			fail++
		}
	}
	fmt.Printf("TOTAL %d/%d\n", len(checks)-fail, len(checks))
	if fail > 0 {
		panic("demo failed")
	}
}
