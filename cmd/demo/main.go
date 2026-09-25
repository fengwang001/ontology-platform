package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	checks := buildChecks()
	pass := 0
	for _, c := range checks {
		status := "FAIL"
		if c.ok {
			status = "OK"
			pass++
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("total %d/%d\n", pass, len(checks))
}

func buildChecks() []check {
	return []check{
		{name: "skeleton", ok: true},
	}
}
