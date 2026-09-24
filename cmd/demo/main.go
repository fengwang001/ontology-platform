package main

import "fmt"

type check struct {
	name string
	ok   bool
}

var checks []check

func record(name string, ok bool) {
	checks = append(checks, check{name, ok})
}

func main() {
	// 判定随 esc / jstr 的实现逐步补入。
	record("skeleton", true)

	fail := 0
	for _, c := range checks {
		status := "OK"
		if !c.ok {
			status = "FAIL"
			fail++
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("total %d/%d ok\n", len(checks)-fail, len(checks))
	if fail != 0 {
		panic("demo failed")
	}
}
