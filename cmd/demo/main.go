package main

import (
	"fmt"
	"os"
)

func ok(name string, pass bool) int {
	if pass {
		fmt.Println("OK  " + name)
		return 0
	}
	fmt.Println("FAIL " + name)
	return 1
}

func main() {
	fails := 0
	fails += ok("skeleton", true)
	fmt.Printf("TOTAL %d step(s), %d fail(s)\n", 1, fails)
	if fails > 0 {
		os.Exit(1)
	}
}
