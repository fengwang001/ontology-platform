// Command demo exercises the escaped-RLE codec end to end.
package main

import (
	"fmt"
	"os"

	"ontology/runs"
)

var failures int

func check(name string, ok bool, detail ...any) {
	if ok {
		fmt.Println("OK  ", name)
		return
	}
	failures++
	fmt.Println("FAIL", name, fmt.Sprint(detail...))
}

func main() {
	rs := runs.Split("aaabéé")
	splitOK := len(rs) == 3 && rs[0] == runs.Run{Rune: 'a', Count: 3} &&
		rs[1] == runs.Run{Rune: 'b', Count: 1} && rs[2] == runs.Run{Rune: 'é', Count: 2} &&
		string(runs.AppendDecimal(nil, 12)) == "12"
	if _, ok := runs.AddDigit(9999999999999999999, 9, 1<<63); ok {
		splitOK = false
	}
	check("runs split+decimal", splitOK)

	fmt.Printf("TOTAL %d failure(s)\n", failures)
	if failures > 0 {
		os.Exit(1)
	}
}
