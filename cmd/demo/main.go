package main

import "fmt"

func main() {
	total, failed := 0, 0
	check := func(name string, ok bool) {
		total++
		if !ok {
			failed++
			fmt.Println("FAIL", name)
			return
		}
		fmt.Println("OK  ", name)
	}

	check("skeleton", true)

	if failed != 0 {
		fmt.Printf("TOTAL %d checks, %d FAILED\n", total, failed)
		return
	}
	fmt.Printf("TOTAL %d checks, all OK\n", total)
}
