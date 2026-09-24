package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"skeleton", true},
	}
	if !printChecks(checks) {
		os.Exit(1)
	}
}

func printChecks(checks []check) bool {
	passed := 0
	for _, c := range checks {
		status := "FAIL"
		if c.ok {
			status = "OK"
		passed++
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("TOTAL %d/%d\n", passed, len(checks))
	return passed == len(checks)
}
