package main

import "fmt"

func main() {
	checks := []struct{ name string }{
		{"skeleton"},
	}
	fail := 0
	for _, c := range checks {
		fmt.Printf("OK %s\n", c.name)
	}
	fmt.Printf("TOTAL %d/%d\n", len(checks)-fail, len(checks))
}
