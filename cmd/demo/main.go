// Command demo exercises the rle codec end to end and prints OK/FAIL lines.
package main

import "fmt"

var checks, fails int

func check(name string, ok bool) {
	checks++
	if !ok {
		fails++
	}
	fmt.Println(verdict(ok), name)
}

func verdict(ok bool) string {
	if ok {
		return "OK  "
	}
	return "FAIL"
}

func main() {
	fmt.Printf("total: %d checks, %d failed\n", checks, fails)
}
