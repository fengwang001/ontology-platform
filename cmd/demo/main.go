package main

import "fmt"

func main() {
	checks := []struct {
		name string
		ok   bool
	}{
		{"separators", false},
		{"comments", false},
		{"continuations", false},
		{"blank continuation", false},
		{"escapes", false},
		{"unicode position", false},
		{"duplicate order", false},
		{"store specials", false},
		{"random roundtrip", false},
		{"scan counter", false},
	}

	failures := 0
	for _, check := range checks {
		status := "OK"
		if !check.ok {
			status = "FAIL"
			failures++
		}
		fmt.Printf("%s: %s\n", check.name, status)
	}
	if failures == 0 {
		fmt.Println("TOTAL: 10/10 OK")
	} else {
		fmt.Printf("TOTAL: %d FAIL\n", failures)
	}
}
