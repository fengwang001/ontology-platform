package main

import "fmt"

func main() {
	fails := 0
	report := func(name string, ok bool) {
		if ok {
			fmt.Println("OK", name)
		} else {
			fails++
			fmt.Println("FAIL", name)
		}
	}
	_ = report

	fmt.Println("OK skeleton")
	if fails != 0 {
		panic("demo failed")
	}
}
