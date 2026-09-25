package main

import "fmt"

func main() {
	pass, total := 0, 0
	check := func(name string, ok bool) {
		total++
		if ok {
			pass++
		}
		fmt.Printf("%s %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], name)
	}
	_ = check
	fmt.Printf("TOTAL %d/%d\n", pass, total)
}
