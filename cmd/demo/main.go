package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"trailing-whitespace samples", false},
		{"=XX not split", false},
		{"76-column limit incl soft '='", false},
		{"five error kinds + offsets", false},
		{"all split points identical", false},
		{"round trip", false},
		{"minimal escaping", false},
		{"check counter <= 2n", false},
	}
	pass := 0
	for _, c := range checks {
		verdict := "FAIL"
		if c.ok {
			verdict, pass = "OK", pass+1
		}
		fmt.Printf("%s  %s\n", verdict, c.name)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		panic("demo checks failed")
	}
}
