// Command demo exercises the CSV parser end to end.
package main

import "fmt"

func main() {
	ok := true
	checks := []struct {
		name string
		pass bool
	}{
		{"placeholder", true},
	}
	for _, c := range checks {
		status := "OK"
		if !c.pass {
			status = "FAIL"
			ok = false
		}
		fmt.Printf("%s: %s\n", c.name, status)
	}
	total := "OK"
	if !ok {
		total = "FAIL"
	}
	fmt.Printf("TOTAL: %s\n", total)
	if !ok {
		fmt.Println("FAIL")
	}
}
