package main

import (
	"fmt"

	"ontology/roll"
)

func main() {
	h, err := roll.New(2)
	check("roll O(1) advance", err == nil)
	h.Add(1)
	h.Add(255)
	v := h.Value()
	h.Advance(1, 2)
	check("roll hash changes", v != h.Value())
}

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK", name)
	} else {
		fmt.Println("FAIL", name)
	}
}
