// Command demo exercises the logical and props packages end to end.
package main

import (
	"fmt"
	"os"

	"ontology/logical"
)

var fails int

func check(name string, ok bool) {
	status := "OK   "
	if !ok {
		status = "FAIL "
		fails++
	}
	fmt.Println(status + name)
}

func main() {
	lines := logical.Lines([]byte("k=v\\\n   w\nk2=v\\\\\nx=y"))
	check("logical continuation", len(lines) == 3 &&
		lines[0] == logical.Line{Text: "k=vw", Num: 1} &&
		lines[1] == logical.Line{Text: `k2=v\\`, Num: 3} &&
		lines[2] == logical.Line{Text: "x=y", Num: 4})
	fmt.Printf("total: %d failure(s)\n", fails)
	if fails > 0 {
		os.Exit(1)
	}
}
