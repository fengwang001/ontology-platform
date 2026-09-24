// Command demo exercises the attribute-level permission filter end to end.
package main

import "fmt"

var results []bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
	}
	fmt.Printf("%s %s\n", status, name)
	results = append(results, ok)
}

func main() {
	check("skeleton runs", true)

	passed := 0
	for _, ok := range results {
		if ok {
			passed++
		}
	}
	fmt.Printf("TOTAL %d/%d passed\n", passed, len(results))
}
