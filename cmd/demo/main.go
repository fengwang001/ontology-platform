package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"skeleton", true},
	}
	fail := false
	for _, c := range checks {
		tag := "OK"
		if !c.ok {
			tag, fail = "FAIL", true
		}
		fmt.Printf("%s %s\n", tag, c.name)
	}
	if fail {
		panic("demo checks failed")
	}
}
