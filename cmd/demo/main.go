package main

import "fmt"

func main() {
	total, passed := 1, 0
	if true {
		fmt.Println("OK skeleton")
		passed++
	}
	fmt.Printf("OK total: %d/%d\n", passed, total)
}
