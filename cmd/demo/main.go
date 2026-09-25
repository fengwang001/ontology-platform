package main

import "fmt"

func main() {
	fails := 0
	check := func(name string, ok bool) {
		if ok {
			fmt.Println("OK   " + name)
		} else {
			fails++
			fmt.Println("FAIL " + name)
		}
	}
	check("skeleton", true)
	if fails > 0 {
		fmt.Printf("TOTAL %d FAIL\n", fails)
		return
	}
	fmt.Println("TOTAL OK")
}
