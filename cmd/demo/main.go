package main

import (
	"fmt"

	"ontology/budget"
	"ontology/pattern"
)

func main() {
	folded, err := pattern.Parse("%%%a")
	if err != nil {
		fmt.Println("FAIL adjacent percent folding: parse error")
		return
	}
	single, err := pattern.Parse("%a")
	if err != nil {
		fmt.Println("FAIL adjacent percent folding: parse error")
		return
	}
	if folded.NormalizedLength() == single.NormalizedLength() && folded.Tokens()[0] == single.Tokens()[0] {
		fmt.Println("OK adjacent percent folding")
	} else {
		fmt.Println("FAIL adjacent percent folding")
	}

	base := budget.New(100)
	for i := 0; i < 1000; i++ {
		counter := budget.New(100)
		if err := counter.Add(7); err != nil || counter.Steps() != 7 {
			fmt.Println("FAIL repeat-1000 budget steps")
			return
		}
	}
	fmt.Println("OK repeat-1000 budget steps", base.Steps())
}
