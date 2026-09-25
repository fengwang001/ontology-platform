package main

import (
	"fmt"
	"os"
)

type judgment struct {
	name string
	pass bool
}

func main() {
	judgments := []judgment{
		{name: "demo skeleton runs", pass: true},
	}
	failed := 0
	for _, j := range judgments {
		status := "OK"
		if !j.pass {
			status = "FAIL"
			failed++
		}
		fmt.Printf("%s %s\n", status, j.name)
	}
	fmt.Printf("TOTAL %d/%d\n", len(judgments)-failed, len(judgments))
	if failed > 0 {
		os.Exit(1)
	}
}
