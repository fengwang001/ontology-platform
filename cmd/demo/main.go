package main

import "fmt"

func main() {
	ok := true
	checks := []struct {
		name string
		pass bool
	}{
		{"skeleton", true},
	}
	for _, c := range checks {
		status := "OK"
		if !c.pass {
			status = "FAIL"
			ok = false
		}
		fmt.Printf("%s: %s\n", status, c.name)
	}
	total := "OK"
	if !ok {
		total = "FAIL"
	}
	fmt.Printf("%s: total %d checks\n", total, len(checks))
}
