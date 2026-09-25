package main

import "fmt"

func main() {
	ok := true
	check := func(name string, pass bool) {
		if pass {
			fmt.Println("OK  " + name)
		} else {
			ok = false
			fmt.Println("FAIL " + name)
		}
	}

	check("skeleton", true)

	if ok {
		fmt.Println("ALL OK")
	} else {
		fmt.Println("SOME FAIL")
	}
}
