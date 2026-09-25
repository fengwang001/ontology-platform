
package main

import (
	"fmt"

	"ontology/rle"
)

var results []bool

func ok(name string, pass bool) {
	results = append(results, pass)
	s := "OK"
	if !pass {
		s = "FAIL"
	}
	fmt.Printf("%-22s %s\n", name, s)
}

func main() {
	ok("format-samples", rle.Encode("aaab") == "3ab")
	pass := 0
	for _, p := range results {
		if p {
			pass++
		}
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(results))
}
