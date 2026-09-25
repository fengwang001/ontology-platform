package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"five errors and offsets", false},
		{"surrogate pair samples", false},
		{"invalid UTF-8 both directions", false},
		{"minimal escaping", false},
		{"round trips", false},
		{"all split points agree", false},
		{"inspection budget", false},
	}

	pass := 0
	for _, item := range checks {
		status := "FAIL"
		if item.ok {
			status = "OK"
			pass++
		}
		fmt.Printf("%s: %s\n", status, item.name)
	}
	fmt.Printf("total: %d/%d\n", pass, len(checks))
}
