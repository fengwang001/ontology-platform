package main

import (
	"fmt"

	"ontology/name"
)

func main() {
	fails := 0
	check := func(ok bool, msg string) {
		if ok {
			fmt.Println("OK " + msg)
		} else {
			fmt.Println("FAIL " + msg)
			fails++
		}
	}

	check(name.Valid("") && name.Valid("a/b"), "name: 空串与含分隔符名字均合法")

	if fails == 0 {
		fmt.Println("TOTAL: 1/1 OK")
	} else {
		fmt.Printf("TOTAL: %d FAIL\n", fails)
	}
}
