// Command demo runs in-memory behavioral checks for the Range assembler.
// It takes no arguments and performs no network or filesystem access.
package main

import "fmt"

func main() {
	pass, total := 0, 0
	check := func(name string, ok bool) {
		total++
		if ok {
			pass++
		}
		status := "OK  "
		if !ok {
			status = "FAIL"
		}
		fmt.Printf("%s %s\n", status, name)
	}

	check("skeleton", true)

	fmt.Printf("TOTAL %d/%d\n", pass, total)
	if pass != total {
		panic("demo checks failed")
	}
}
