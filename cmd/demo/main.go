package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{{name: "skeleton", ok: true}}
	for _, item := range checks {
		if item.ok {
			fmt.Println("OK", item.name)
			continue
		}
		fmt.Println("FAIL", item.name)
	}
}
