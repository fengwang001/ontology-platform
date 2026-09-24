// Command demo prints OK/FAIL checks for the jstr strict JSON string codec.
package main

import "fmt"

var passed, total int

func check(name string, ok bool) {
	total++
	if ok {
		passed++
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
	}
}

func main() {
	fmt.Printf("PASS %d/%d\n", passed, total)
}
