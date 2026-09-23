package main

import "fmt"

func main() {
	results := []bool{true}
	ok := 0
	fmt.Println("OK skeleton")
	for _, r := range results {
		if r {
			ok++
		}
	}
	fmt.Printf("TOTAL %d/%d\n", ok, len(results))
}
