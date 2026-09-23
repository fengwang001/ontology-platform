package main

import (
	"fmt"
	"os"

	"ontology/chunk"
)

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{}
	checks = append(checks, check{"chunk splits non-ASCII text without panic", chunkNonASCII()})

	failed := 0
	for _, c := range checks {
		status := "OK"
		if !c.ok {
			status = "FAIL"
			failed++
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("TOTAL %d/%d\n", len(checks)-failed, len(checks))
	if failed != 0 {
		os.Exit(1)
	}
}

func chunkNonASCII() bool {
	segs := chunk.Split("a１２🙂9b")
	if len(segs) != 4 {
		return false
	}
	return segs[0].Kind == chunk.Text && segs[0].Text == "a" &&
		segs[1].Kind == chunk.Text &&
		segs[2].Kind == chunk.Digit && segs[2].Text == "9" &&
		segs[3].Kind == chunk.Text
}
