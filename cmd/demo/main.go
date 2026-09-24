package main

import "fmt"
import "ontology/iv"

func main() {
	a, _ := iv.New(1, 3)
	b, _ := iv.New(3, 5)
	c, _ := iv.New(7, 9)
	if a.Adjacent(b) && !a.Overlaps(c) {
		fmt.Println("OK iv relations")
		return
	}
	fmt.Println("FAIL iv relations")
}
