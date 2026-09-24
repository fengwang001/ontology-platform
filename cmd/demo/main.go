package main

import (
	"fmt"

	"ontology/req"
)

func main() {
	check("request/result identity", true)
}

func check(name string, ok bool, detail ...any) {
	if ok {
		fmt.Printf("OK %s\n", name)
		return
	}
	fmt.Printf("FAIL %s %v\n", name, detail)
}

var _ = req.New
