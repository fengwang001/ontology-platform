package main

import "fmt"

func ok(name string, pass bool) {
	if pass {
		fmt.Println("OK  " + name)
		return
	}
	fmt.Println("FAIL " + name)
}

func main() {
	ok("skeleton", true)
}
