package main

import "fmt"

func main() {
	fails := 0
	check := func(name string, ok bool) {
		if ok {
			fmt.Println("OK", name)
			return
		}
		fails++
		fmt.Println("FAIL", name)
	}

	check("skeleton", true)
	if fails == 0 {
		fmt.Println("total: all pass")
	} else {
		fmt.Printf("total: %d fail\n", fails)
	}
}
