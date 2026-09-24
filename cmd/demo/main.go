package main

import "fmt"

func main() {
	total, passed := 0, 0
	check := func(name string, ok bool) {
		total++
		if ok {
			passed++
			fmt.Println("OK  " + name)
		} else {
			fmt.Println("FAIL " + name)
		}
	}

	check("skeleton", true)

	if passed == total {
		fmt.Printf("TOTAL %d/%d OK\n", passed, total)
	} else {
		fmt.Printf("TOTAL %d/%d FAIL\n", passed, total)
	}
}
