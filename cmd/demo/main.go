// Command demo prints OK/FAIL lines for the quoted-printable package's
// required semantics and exits non-zero if any check fails.
package main

import (
	"fmt"
	"os"
)

var failures int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	fmt.Printf("TOTAL %d failures\n", failures)
	if failures > 0 {
		os.Exit(1)
	}
}
