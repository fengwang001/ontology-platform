package main

import (
	"fmt"

	"ontology/txn"
)

func main() {
	reg := txn.NewRegistry()
	t := reg.Begin()
	if err := reg.Commit(t, 1); err != nil {
		fmt.Println("FAIL txn commit:", err)
		return
	}
	if info, ok := reg.Lookup(t); ok && info.Status == txn.Committed {
		fmt.Println("OK txn commit recorded")
	} else {
		fmt.Println("FAIL txn commit recorded")
	}
}
