package main

import "fmt"

func main() {
	checks := []struct {
		name string
		ok   bool
	}{
		{"five error classes and offsets", false},
		{"surrogate pair rules", false},
		{"invalid UTF-8 both directions", false},
		{"minimal escaping", false},
		{"round trip", false},
		{"every split point", false},
		{"byte-check counter", false},
	}

	failed := 0
	for _, check := range checks {
		status := "OK"
		if !check.ok {
			status = "FAIL"
			failed++
		}
		fmt.Printf("%s: %s\n", status, check.name)
	}
	fmt.Printf("TOTAL: %d/%d\n", len(checks)-failed, len(checks))
}
