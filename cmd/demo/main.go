package main

import "fmt"

func report(name string, ok bool) bool {
	if ok {
		fmt.Println("OK", name)
	} else {
		fmt.Println("FAIL", name)
	}
	return ok
}

func main() {
	checks := []bool{
		report("trailing-space-newline", false),
		report("trailing-space-before-byte", false),
		report("trailing-space-eof", false),
		report("encoded-token-unsplit", false),
		report("76-column-limit", false),
		report("five-distinct-errors", false),
		report("all-split-boundaries", false),
		report("round-trip", false),
		report("minimal-escaping", false),
		report("input-check-counter", false),
	}

	passed := 0
	for _, ok := range checks {
		if ok {
			passed++
		}
	}
	fmt.Printf("total: %d/%d OK\n", passed, len(checks))
}
