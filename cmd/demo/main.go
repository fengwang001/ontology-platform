package main

import "fmt"

func main() {
	pass, total := 0, 1
	check := func(name string, ok bool) {
		total++
		if ok {
			pass++
			fmt.Println("OK  ", name)
		} else {
			fmt.Println("FAIL", name)
		}
	}
	check("skeleton", true)
	if pass == total-1 {
		fmt.Printf("TOTAL %d/%d OK\n", pass, total-1)
		return
	}
	fmt.Printf("TOTAL %d/%d FAIL\n", pass, total-1)
}
