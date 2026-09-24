package main

import "fmt"

func main() {
	total, failed := 0, 0
	report := func(name string, ok bool) {
		total++
		if !ok {
			failed++
		}
		fmt.Printf("%s: %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], name)
	}

	report("skeleton", true)
	fmt.Printf("TOTAL %d FAIL %d\n", total, failed)
}
