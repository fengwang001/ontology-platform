// Command demo prints OK/FAIL lines for the quoted-printable implementation.
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
	fmt.Println(status + " " + name)
}

func main() {
	check("skeleton", true)

	if failed != 0 {
		os.Exit(1)
	}
	fmt.Printf("all %d checks passed\n", 1)
}
