package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"errors", false},
		{"surrogates", false},
		{"invalid-utf8", false},
		{"minimal-escape", false},
		{"roundtrip", false},
		{"splits", false},
		{"counter", false},
	}
	pass := 0
	for _, item := range checks {
		status := "FAIL"
		if item.ok {
			status = "OK"
			pass++
		}
		fmt.Printf("%s %s\n", status, item.name)
	}
	fmt.Printf("total %d/%d\n", pass, len(checks))
}
