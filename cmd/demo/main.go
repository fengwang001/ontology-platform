package main

import "fmt"

func main() {
	total := 0
	check := func(name string, ok bool) {
		total++
		if ok {
			fmt.Println("OK  ", name)
		} else {
			fmt.Println("FAIL", name)
		}
	}

	check("skeleton", true)
	fmt.Printf("TOTAL %d\n", total)
}
