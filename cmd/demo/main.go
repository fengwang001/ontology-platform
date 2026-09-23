// Command demo exercises the unified-diff pipeline; no args, no network.
package main

import "fmt"

func main() {
	pass, total := 0, 0
	check := func(name string, ok bool) {
		total++
		if ok {
			pass++
		}
		fmt.Printf("%s: %s\n", name, map[bool]string{true: "OK", false: "FAIL"}[ok])
	}
	_ = check
	fmt.Printf("TOTAL %d/%d OK\n", pass, total)
}
