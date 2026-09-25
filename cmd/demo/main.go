package main

import "fmt"

func report(name string, ok bool) bool {
	if ok {
		fmt.Println("OK   " + name)
	} else {
		fmt.Println("FAIL " + name)
	}
	return ok
}

func main() {
	all := true
	all = report("skeleton", true) && all
	if all {
		fmt.Println("ALL OK: 1/1")
	} else {
		fmt.Println("SOME FAILED")
	}
}
