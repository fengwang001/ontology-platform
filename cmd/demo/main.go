package main

import "fmt"

func main() {
	checks := []struct {
		name string
		ok   bool
	}{
		{"format", false},
		{"roundtrip", false},
		{"rejects", false},
		{"large-count", false},
		{"combining", false},
		{"splits", false},
		{"inspection-count", false},
	}

	pass := 0
	for _, check := range checks {
		status := "FAIL"
		if check.ok {
			status, pass = "OK", pass+1
		}
		fmt.Printf("%s %s\n", status, check.name)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
}
