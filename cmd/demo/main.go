package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"format", false},
		{"roundtrip", false},
		{"rejects", false},
		{"large-count", false},
		{"combining", false},
		{"splits", false},
		{"counter", false},
	}
	pass := 0
	for _, c := range checks {
		status := "FAIL"
		if c.ok {
			status, pass = "OK", pass+1
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("total %d/%d\n", pass, len(checks))
}
