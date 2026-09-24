package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"separators and whitespace", false},
		{"comments", false},
		{"continuation backslash parity", false},
		{"continuation strips indentation", false},
		{"triple-backslash continuation", false},
		{"continuation hash is data", false},
		{"blank continuation line", false},
		{"escapes and unicode position", false},
		{"duplicate key order", false},
		{"store special characters", false},
		{"1000 random round trips", false},
		{"byte inspection counter", false},
	}

	failed := 0
	for _, item := range checks {
		status := "OK"
		if !item.ok {
			status = "FAIL"
			failed++
		}
		fmt.Printf("%s: %s\n", status, item.name)
	}
	fmt.Printf("TOTAL: %d/%d\n", len(checks)-failed, len(checks))
}
