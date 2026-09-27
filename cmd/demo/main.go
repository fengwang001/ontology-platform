package main

import "fmt"

func main() {
	total, fail := 0, 0
	check := func(name string, ok bool) {
		total++
		if !ok {
			fail++
		}
		status := "OK"
		if !ok {
			status = "FAIL"
		}
		fmt.Printf("%-28s %s\n", name, status)
	}

	check("skeleton", true)

	if fail != 0 {
		fmt.Printf("TOTAL %d (%d FAIL)\n", total, fail)
		panic("demo failed")
	}
	fmt.Printf("TOTAL %d all OK\n", total)
}
