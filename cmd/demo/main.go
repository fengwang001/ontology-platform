package main

import "fmt"

var checks int

func judge(name string, ok bool) {
	checks++
	if ok {
		fmt.Printf("OK   %s\n", name)
	} else {
		fmt.Printf("FAIL %s\n", name)
	}
}

func main() {
	judge("skeleton runs", true)
	fmt.Printf("OK   total %d checks\n", checks)
}
