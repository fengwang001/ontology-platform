// Command demo prints OK/FAIL lines for the RLE codec requirements.
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
		{"reject list with offsets", false},
		{"huge count", false},
		{"combining marks not merged", false},
		{"all split points identical", false},
		{"byte counter", false},
	}

	fails := 0
	for _, c := range checks {
		status := "OK"
		if !c.ok {
			status, fails = "FAIL", fails+1
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("total %d/%d OK\n", len(checks)-fails, len(checks))
	if fails > 0 {
		panic("demo checks failed")
	}
}
