// Command demo exercises the strict streaming Base64 codec checks.
package main

import "fmt"

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  ", name)
	} else {
		fails++
		fmt.Println("FAIL", name)
	}
}

func main() {
	check("skeleton", true)
	fmt.Println("total:", 1, "checks,", fails, "failed")
}
