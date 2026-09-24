package main

import "fmt"

func main() {
	checks := []struct {
		name string
		ok   bool
	}{
		{"skeleton", true},
	}
	pass := 0
	for _, c := range checks {
		status := "FAIL"
		if c.ok {
			status, pass = "OK", pass+1
		}
		fmt.Printf("%-40s %s\n", c.name, status)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
}
