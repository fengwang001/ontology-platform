// Command demo exercises the properties parser/writer.
package main

import (
	"fmt"
	"os"
)

var failures int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  ", name)
		return
	}
	failures++
	fmt.Println("FAIL", name)
}

func main() {
	check("skeleton", true)

	fmt.Printf("total: %d failure(s)\n", failures)
	if failures > 0 {
		os.Exit(1)
	}
}
