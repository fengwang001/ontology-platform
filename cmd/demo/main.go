// Command demo exercises the rle package end to end. Checks are added
// incrementally as each package part is implemented.
package main

import "fmt"

var fails int

func check(name string, ok bool, detail ...any) {
	if ok {
		fmt.Println("OK  ", name)
		return
	}
	fails++
	fmt.Println("FAIL", name, detail)
}

func main() {
	check("skeleton", true)
	fmt.Println("== 1 checks,", fails, "fail ==")
}
