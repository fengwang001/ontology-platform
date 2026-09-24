package main

import (
	"fmt"

	"ontology/mono"
	"ontology/stack"
)

func ok(label string, pass bool) {
	if pass {
		fmt.Println("OK", label)
		return
	}
	fmt.Println("FAIL", label)
}

func main() {
	indices := stack.New()
	indices.Push(0)
	top, hasTop := indices.Top()
	popped, hasPopped := indices.Pop()
	ok("stack", hasTop && top == 0 && hasPopped && popped == 0 && indices.Len() == 0)

	scanner, err := mono.NewScanner(8)
	answers, scanErr := scanner.NextGreater([]int{2, 2, 3})
	ok("mono", err == nil && scanErr == nil && fmt.Sprint(answers) == "[2 2 -1]")
}
