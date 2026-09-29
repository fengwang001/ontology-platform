package main

import (
	"fmt"

	"ontology/roll"
)

func main() {
	h, err := roll.New(2)
	if err != nil || h.Push('a') != 97 || h.Push('b') != 97*257+98 {
		fmt.Println("rolling hash FAIL")
		return
	}
	fmt.Println("rolling hash OK")
}
