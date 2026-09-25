package main

import "fmt"

func main() {
	pass := true
	check("skeleton runs", true, &pass)
	total(pass)
}

func check(name string, ok bool, pass *bool) {
	if ok {
		fmt.Printf("OK %s\n", name)
		return
	}
	*pass = false
	fmt.Printf("FAIL %s\n", name)
}

func total(pass bool) {
	if pass {
		fmt.Println("OK total")
		return
	}
	fmt.Println("FAIL total")
}
