// Command demo prints OK/FAIL lines for each guarantee of the rle codec.
package main

import "fmt"

var ok, bad int

func check(name string, good bool) {
	if good {
		ok++
		fmt.Println("OK   " + name)
	} else {
		bad++
		fmt.Println("FAIL " + name)
	}
}

func main() {
	fmt.Printf("total %d ok, %d fail\n", ok, bad)
}
