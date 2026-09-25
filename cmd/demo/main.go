package main

import (
	"fmt"
	"os"
)

var failed int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed++
	}
	fmt.Printf("%-24s %s\n", name, status)
}

func main() {
	fmt.Printf("TOTAL %d failed\n", failed)
	if failed > 0 {
		os.Exit(1)
	}
}
