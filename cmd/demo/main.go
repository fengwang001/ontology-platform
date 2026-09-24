package main

import "fmt"

type result struct {
	name string
	ok   bool
}

func main() {
	results := []result{
		{"trailing-whitespace 3 samples", false},
		{"=XX never split", false},
		{"76-column limit counts soft '='", false},
		{"5 distinct errors with offsets", false},
		{"all split points consistent", false},
		{"round trip", false},
		{"minimal escaping", false},
		{"inspection counter <= 2N", false},
	}
	pass := 0
	for _, r := range results {
		mark := "FAIL"
		if r.ok {
			mark, pass = "OK", pass+1
		}
		fmt.Printf("%s %s\n", mark, r.name)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(results))
}
