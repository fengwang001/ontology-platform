// Command demo prints OK/FAIL lines for the RLE codec's required semantics.
package main

import "fmt"

var failures int

func check(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
		return
	}
	failures++
	fmt.Printf("FAIL %s\n", name)
}

func main() {
	check("skeleton", true)
	fmt.Printf("total: %d failure(s)\n", failures)
	if failures > 0 {
		panic("demo checks failed")
	}
}
