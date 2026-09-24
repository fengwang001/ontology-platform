package main

import "fmt"

func main() {
	passed := 0
	failed := 0

	check := func(name string, ok bool) {
		if ok {
			passed++
			fmt.Printf("OK %s\n", name)
			return
		}
		failed++
		fmt.Printf("FAIL %s\n", name)
	}

	check("skeleton", true)
	fmt.Printf("total: %d OK, %d FAIL\n", passed, failed)
	if failed != 0 {
		panic("demo failed")
	}
}
