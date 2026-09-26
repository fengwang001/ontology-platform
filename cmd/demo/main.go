package main

import "fmt"

func main() {
	total, passed := 0, 0
	check := func(name string, ok bool) {
		total++
		if ok {
			passed++
		}
		fmt.Printf("%s: %s\n", name, map[bool]string{true: "OK", false: "FAIL"}[ok])
	}

	check("skeleton", true)
	fmt.Printf("TOTAL %d/%d OK\n", passed, total)
	if passed != total {
		panic("demo failed")
	}
}
