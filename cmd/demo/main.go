package main

import "fmt"

func ok(name string, good bool) {
	if good {
		fmt.Println("OK  ", name)
	} else {
		fmt.Println("FAIL", name)
	}
}

func main() {
	ok("format", false)
	ok("roundtrip", false)
	ok("reject-errors", false)
	ok("big-count", false)
	ok("combining", false)
	ok("all-splits", false)
	ok("examined-counter", false)
	fmt.Println("TOTAL 0/7")
}
