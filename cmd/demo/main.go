package main

import "fmt"

func report(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
		return
	}
	fmt.Printf("FAIL %s\n", name)
}

func main() {
	report("skeleton", true)
}
