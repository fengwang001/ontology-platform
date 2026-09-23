package main

import "fmt"

type check struct {
	name string
	fn   func() bool
}

func main() {
	checks := []check{
		{"trailing-ws before newline", func() bool { return false }},
		{"interior space kept literal", func() bool { return false }},
		{"trailing-ws at EOF", func() bool { return false }},
		{"=XX never split by soft break", func() bool { return false }},
		{"line cap 76 incl soft-break =", func() bool { return false }},
		{"five error kinds distinct w/ offset", func() bool { return false }},
		{"all split points identical", func() bool { return false }},
		{"roundtrip Decode(Encode(x))=N(x)", func() bool { return false }},
		{"minimal escaping (idempotent)", func() bool { return false }},
		{"counter <= 2*input bytes", func() bool { return false }},
	}
	pass := 0
	for _, c := range checks {
		status := "FAIL"
		if c.fn() {
			status, pass = "OK", pass+1
		}
		fmt.Printf("%s  %s\n", status, c.name)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		fmt.Println("DEMO FAILED")
	}
}
