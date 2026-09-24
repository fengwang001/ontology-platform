package main

import (
	"fmt"
	"os"

	"ontology/etime"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	check("ingest check count O(1) for large m", etime.SelfCheck())
	if failed {
		os.Exit(1)
	}
}
