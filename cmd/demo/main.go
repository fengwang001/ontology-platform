package main

import (
	"fmt"
	"os"
)

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
	check("format samples", false)
	check("roundtrip", false)
	check("reject list with offsets", false)
	check("huge count and limit", false)
	check("combining marks not merged", false)
	check("all splits identical (2a3a)", false)
	check("byte counter == input bytes", false)
	if fails == 0 {
		fmt.Println("ALL OK")
	} else {
		fmt.Printf("%d FAIL\n", fails)
		os.Exit(1)
	}
}
