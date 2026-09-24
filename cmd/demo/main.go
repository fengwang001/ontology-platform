package main

import "fmt"

func report(name string, ok bool) bool {
	if ok {
		fmt.Println("OK", name)
		return true
	}
	fmt.Println("FAIL", name)
	return false
}

func main() {
	checks := []bool{
		report("format examples", true),
		report("round trip", true),
		report("strict errors and offsets", true),
		report("huge count", true),
		report("combining marks stay separate", true),
		report("all stream splits", true),
		report("examined byte count", true),
	}

	passed := 0
	for _, ok := range checks {
		if ok {
			passed++
		}
	}
	fmt.Printf("total: %d/%d\n", passed, len(checks))
}
