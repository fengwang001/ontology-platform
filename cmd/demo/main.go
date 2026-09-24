// Command demo exercises the strict streaming base64 packages.
package main

import "fmt"

var fails int

func check(name string, ok bool) {
	if !ok {
		fails++
	}
	fmt.Printf("%s %s\n", map[bool]string{true: "OK  ", false: "FAIL"}[ok], name)
}

func main() {
	fmt.Println("demo skeleton: packages not wired yet")
}
