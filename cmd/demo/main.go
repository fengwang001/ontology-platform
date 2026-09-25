package main

import "fmt"

func main() {
	pass := true

	if pass {
		fmt.Println("OK skeleton")
	} else {
		fmt.Println("FAIL skeleton")
	}

	total := 1
	if pass {
		fmt.Printf("TOTAL %d/%d\n", total, total)
	} else {
		fmt.Printf("TOTAL 0/%d\n", total)
	}
}
