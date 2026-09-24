package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"format examples", false},
		{"roundtrip", false},
		{"reject list + offsets", false},
		{"huge count", false},
		{"combining marks not merged", false},
		{"all split points incl 2a|3a", false},
		{"inspection counter", false},
	}
	pass := 0
	for _, c := range checks {
		mark := "FAIL"
		if c.ok {
			mark, pass = "OK", pass+1
		}
		fmt.Printf("%s: %s\n", mark, c.name)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		panic("demo failed")
	}
}
