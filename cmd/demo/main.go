package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{{name: "skeleton", ok: true}}
	fail := 0
	for _, c := range checks {
		status := "OK"
		if !c.ok {
			status, fail = "FAIL", fail+1
		}
		fmt.Printf("%-4s %s\n", status, c.name)
	}
	if fail != 0 {
		fmt.Println("FAILURES:", fail)
	}
}
