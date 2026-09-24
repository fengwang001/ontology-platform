package main

import "fmt"

func main() {
	pass := true
	check("skeleton", true, &pass)
	if !pass {
		panic("demo failed")
	}
}

func check(name string, ok bool, pass *bool) {
	if ok {
		fmt.Println("OK", name)
		return
	}
	*pass = false
	fmt.Println("FAIL", name)
}
