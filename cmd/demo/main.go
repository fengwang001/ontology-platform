package main

import "fmt"

func main() {
	total, failed := 0, 0
	check := func(name string, ok bool) {
		total++
		if !ok {
			failed++
			fmt.Printf("FAIL %s\n", name)
			return
		}
		fmt.Printf("OK %s\n", name)
	}

	check("skeleton", true)

	if failed == 0 {
		fmt.Printf("TOTAL %d/%d OK\n", total, total)
		return
	}
	fmt.Printf("TOTAL %d/%d FAILED\n", total-failed, total)
}
