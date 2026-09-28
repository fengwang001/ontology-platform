package main

import "fmt"

var names []string
var oks []bool

func main() {
	add := func(name string, ok bool) {
		names, oks = append(names, name), append(oks, ok)
	}

	add("skeleton", true)

	fail := 0
	for i, name := range names {
		if oks[i] {
			fmt.Println("OK  ", name)
		} else {
			fail++
			fmt.Println("FAIL", name)
		}
	}
	if fail == 0 {
		fmt.Printf("TOTAL %d/%d OK\n", len(names), len(names))
	} else {
		fmt.Printf("TOTAL %d/%d OK (%d FAIL)\n", len(names)-fail, len(names), fail)
	}
}
