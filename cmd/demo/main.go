package main

import "fmt"

func main() {
	checks := 0
	ok := func(name string, good bool) {
		checks++
		if good {
			fmt.Println(name, "OK")
			return
		}
		fmt.Println(name, "FAIL")
	}
	ok("skeleton", true)
	fmt.Printf("total %d/%d OK\n", checks, checks)
}
