package main

import "fmt"

func report(name string, ok bool) {
	if ok {
		fmt.Println("OK   " + name)
		return
	}
	fmt.Println("FAIL " + name)
}

func main() {
	report("format samples", false)
	report("round-trip", false)
	report("reject list with offsets", false)
	report("large count handling", false)
	report("combining mark not merged", false)
	report("all split points (incl. 2a|3a)", false)
	report("inspected-byte counter", false)

	fmt.Println("total: 0/7 OK")
}
