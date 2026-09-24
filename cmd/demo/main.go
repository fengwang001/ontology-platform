package main

import "fmt"

func main() {
	checks := []bool{}
	fmt.Println("demo pending")
	if len(checks) != 0 {
		panic("unreachable")
	}
}
