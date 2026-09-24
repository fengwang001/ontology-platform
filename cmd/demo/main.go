package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	var checks []check

	// 后续每实现一个包，在此补一条真实判定。
	checks = append(checks, check{"skeleton", true})

	fail := 0
	for _, c := range checks {
		status := "OK"
		if !c.ok {
			status = "FAIL"
			fail++
		}
		fmt.Printf("%-22s %s\n", c.name, status)
	}
	fmt.Printf("total: %d/%d OK\n", len(checks)-fail, len(checks))
	if fail > 0 {
		panic("demo failed")
	}
}
