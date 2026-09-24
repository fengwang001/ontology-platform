package main

import (
	"errors"
	"fmt"

	"ontology/batch"
)

func main() {
	fails := 0
	check := func(name string, ok bool) {
		if ok {
			fmt.Println("OK  " + name)
		} else {
			fmt.Println("FAIL " + name)
			fails++
		}
	}

	dup := (&batch.Batch{ID: "b", Records: []batch.Record{{Key: "x"}, {Key: "x"}}}).Validate()
	var dupErr *batch.DupKeyError
	check("batch rejects in-batch duplicate key with both positions", errors.As(dup, &dupErr) && dupErr.First == 0 && dupErr.Second == 1)

	if fails == 0 {
		fmt.Println("TOTAL: all checks OK")
	} else {
		fmt.Printf("TOTAL: %d FAIL\n", fails)
	}
}
