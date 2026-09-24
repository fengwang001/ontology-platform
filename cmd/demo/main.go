// Command demo exercises the strict streaming Base64 codec.
package main

import (
	"fmt"
	"os"
)

var failed int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  ", name)
		return
	}
	failed++
	fmt.Println("FAIL", name)
}

func main() {
	check("skeleton", true)
	fmt.Printf("total: %d failed\n", failed)
	if failed > 0 {
		os.Exit(1)
	}
}
