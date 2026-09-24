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
	for _, check := range checks {
		status := "FAIL"
		if check.ok {
			status = "OK"
			pass++
		}
		fmt.Printf("%s %s\n", status, check.name)
	}
	fmt.Printf("total %d/%d\n", pass, len(checks))
}
