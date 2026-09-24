package main

import (
	"fmt"

	nm "ontology/name"
)

func main() {
	fails := 0
	total := 0
	check := func(desc string, ok bool) {
		total++
		if ok {
			fmt.Println("OK  " + desc)
			return
		}
		fails++
		fmt.Println("FAIL " + desc)
	}

	check("name: empty string valid, slash plain, NUL invalid",
		nm.Valid("") && nm.Valid("a/b") && !nm.Valid("a\x00b"))
	check("name: set equality is order-independent",
		nm.EqualAsSet(nm.New("a", "", "b"), nm.New("b", "a", "")))

	if fails == 0 {
		fmt.Printf("TOTAL: all %d checks passed\n", total)
	} else {
		fmt.Printf("TOTAL: %d/%d checks FAILED\n", fails, total)
	}
}
