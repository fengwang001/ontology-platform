package main

import "fmt"

func main() {
	checks := []struct {
		name string
		ok   bool
	}{
		{"format", false},
		{"roundtrip", false},
		{"errors", false},
		{"large count", false},
		{"combining", false},
		{"splits", false},
		{"counter", false},
	}

	pass := 0
	for _, check := range checks {
		status := "FAIL"
		if check.ok {
			status = "OK"
			pass++
		}
		fmt.Printf("%s %s\n", status, check.name)
	}
	fmt.Printf("%d/%d checks passed\n", pass, len(checks))
	if pass != len(checks) {
		panic("demo checks failed")
	}
}
