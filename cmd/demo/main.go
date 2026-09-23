package main

import "fmt"

func report(name string, ok bool) {
	if ok {
		fmt.Println("OK  ", name)
	} else {
		fmt.Println("FAIL", name)
	}
}

func main() {
	report("skeleton", true)
}
