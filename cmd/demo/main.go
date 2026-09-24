package main

import (
	"errors"
	"fmt"

	"ontology/shard"
)

func main() {
	fails := 0
	check("skeleton runs", true, &fails)
	bad := shard.Partial{Claimed: 2, Records: []shard.Record{{ID: "x"}}}
	check("corrupt payload rejected", errors.Is(shard.Validate(bad), shard.ErrCorrupt), &fails)
	if fails == 0 {
		fmt.Println("TOTAL: 2/2 OK")
		return
	}
	fmt.Printf("TOTAL: %d FAIL\n", fails)
}

func check(name string, ok bool, fails *int) {
	if ok {
		fmt.Println("OK  " + name)
		return
	}
	*fails++
	fmt.Println("FAIL " + name)
}
