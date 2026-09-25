package main

import "fmt"

type check struct {
	name string
	ok   bool
}

var checks []check

func add(name string, ok bool) { checks = append(checks, check{name, ok}) }

func main() {
	// 判定随 esc / jstr 的实现逐条补入。

	pass := 0
	for _, c := range checks {
		status := "FAIL"
		if c.ok {
			status, pass = "OK", pass+1
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
}
