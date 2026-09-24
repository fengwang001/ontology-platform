package main

import (
	"fmt"

	"ontology/change"
)

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"change encode/decode", testChange()},
	}
	failed := 0
	for _, item := range checks {
		status := "OK"
		if !item.ok {
			status = "FAIL"
			failed++
		}
		fmt.Printf("%s %s\n", status, item.name)
	}
	fmt.Printf("TOTAL %d/%d\n", len(checks)-failed, len(checks))
	if failed != 0 {
		panic("demo failed")
	}
}

func testChange() bool {
	original := change.Change{
		Version:  1,
		Op:       change.Insert,
		ID:       "r1",
		NewGroup: "g",
		NewValue: 1.5,
		HasNew:   true,
	}
	data, err := change.Encode(original)
	if err != nil {
		return false
	}
	decoded, err := change.Decode(data)
	return err == nil && decoded == original
}
