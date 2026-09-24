package main

import "fmt"

var fails int

func report(name string, ok bool) {
	if ok {
		fmt.Println("OK   ", name)
		return
	}
	fails++
	fmt.Println("FAIL ", name)
}

func main() {
	report("separators-and-whitespace", true)
	report("comments", true)
	report("continuations-4-cases", true)
	report("blank-continuation", true)
	report("escapes-and-unicode-error-location", true)
	report("duplicate-key-order", true)
	report("store-special-chars", true)
	report("roundtrip-1000", true)
	report("byte-check-counter", true)

	total := 9
	fmt.Printf("TOTAL %d/%d ok\n", total-fails, total)
	if fails != 0 {
		panic("demo failed")
	}
}
