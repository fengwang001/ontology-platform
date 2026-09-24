package main

import "fmt"

func main() {
	fails := 0
	check := func(name string, ok bool) {
		if ok {
			fmt.Println("OK  ", name)
		} else {
			fmt.Println("FAIL", name)
			fails++
		}
	}

	check("demo skeleton", true)

	if fails > 0 {
		fmt.Printf("TOTAL: %d FAIL\n", fails)
	} else {
		fmt.Println("TOTAL: all OK")
	}
}
