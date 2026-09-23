package main

import "fmt"

func main() {
	pass, total := 0, 1
	check("skeleton", true, &pass)
	fmt.Printf("TOTAL %d/%d OK\n", pass, total)
	if pass != total {
		panic("demo failed")
	}
}

func check(name string, ok bool, pass *int) {
	if ok {
		*pass++
		fmt.Printf("OK   %s\n", name)
		return
	}
	fmt.Printf("FAIL %s\n", name)
}
