package main

import "fmt"

func main() {
	checks := []string{
		"separators-and-whitespace",
		"comments",
		"continuations",
		"escapes-and-unicode-errors",
		"duplicate-key-order",
		"store-specials",
		"roundtrip-1000",
		"checked-byte-counter",
	}
	passed := 0
	for _, name := range checks {
		ok := false
		if ok {
			passed++
		}
		fmt.Printf("%-30s %s\n", name, result(ok))
	}
	fmt.Printf("TOTAL %d/%d\n", passed, len(checks))
	if passed != len(checks) {
		panic("fail")
	}
}

func result(ok bool) string {
	if ok {
		return "OK"
	}
	return "FAIL"
}
