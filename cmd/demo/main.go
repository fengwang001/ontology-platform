package main

import "fmt"

func main() {
	pass, total := 0, 0
	check := func(name string, ok bool) {
		total++
		if ok {
			pass++
			fmt.Printf("OK   %s\n", name)
		} else {
			fmt.Printf("FAIL %s\n", name)
		}
	}

	check("skeleton", true)

	if pass == total {
		fmt.Printf("TOTAL %d/%d OK\n", pass, total)
	} else {
		fmt.Printf("TOTAL %d/%d FAIL\n", pass, total)
	}
}
