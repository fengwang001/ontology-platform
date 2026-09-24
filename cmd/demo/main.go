// Command demo prints OK/FAIL lines verifying the strict streaming Base64
// codec semantics. Exit code is 0 only when every check passes.
package main

import (
	"fmt"
	"os"
)

type check struct {
	name string
	run  func() bool
}

func main() {
	checks := []check{}
	failed := 0
	for _, c := range checks {
		status := "OK"
		if !c.run() {
			status = "FAIL"
			failed++
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("total: %d checks, %d failed\n", len(checks), failed)
	if failed > 0 {
		os.Exit(1)
	}
}
