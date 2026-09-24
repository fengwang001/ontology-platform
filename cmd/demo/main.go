package main

import "fmt"

func report(name string, ok bool) {
	if ok {
		fmt.Println("OK", name)
		return
	}
	fmt.Println("FAIL", name)
}

func main() {
	checks := []struct {
		name string
		ok   bool
	}{
		{"skeleton", true},
	}
	pass := 0
	for _, check := range checks {
		report(check.name, check.ok)
		if check.ok {
			pass++
		}
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
}
