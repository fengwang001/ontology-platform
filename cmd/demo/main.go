package main

import (
	"fmt"
	"os"

	"ontology/row"
)

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"复合键: 等值组按ID定序, 严格大于可切开", checkRowTie()},
	}
	failed := 0
	for _, c := range checks {
		if c.ok {
			fmt.Println("OK  " + c.name)
		} else {
			fmt.Println("FAIL " + c.name)
			failed++
		}
	}
	fmt.Printf("总计 %d/%d 通过\n", len(checks)-failed, len(checks))
	if failed > 0 {
		os.Exit(1)
	}
}

func checkRowTie() bool {
	tied := []row.Row{{1, "c"}, {1, "a"}, {1, "b"}}
	// Composite key (1,"b") strictly separates a,b from c within the tie.
	if !row.Before(tied[1], 1, "b") || row.Before(tied[2], 1, "b") {
		return false
	}
	return row.Compare(tied[1], row.Row{1, "a"}) == 0 &&
		row.Compare(tied[0], row.Row{1, "a"}) > 0
}
