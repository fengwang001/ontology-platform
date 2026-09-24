// Command demo prints OK/FAIL checks for the qp package.
package main

import (
	"fmt"
	"os"
)

var failed int

func check(name string, ok bool) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		failed++
	}
	fmt.Println(status, name)
}

func main() {
	check("skeleton", true)
	fmt.Printf("total %d/1\n", 1-failed)
	if failed > 0 {
		os.Exit(1)
	}
}
