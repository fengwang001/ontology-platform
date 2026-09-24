package main

import (
	"context"
	"fmt"

	"ontology/shard"
)

var pass, fail int

func check(name string, ok bool) {
	if ok {
		pass++
		fmt.Println("OK  " + name)
	} else {
		fail++
		fmt.Println("FAIL " + name)
	}
}

func main() {
	ctx := context.Background()

	corrupt := shard.NewFake("c", []shard.Record{{ID: "r1"}}, shard.WithCorrupt())
	dup := shard.NewFake("d", []shard.Record{{ID: "a", Value: 1}}, shard.WithDuplicate())
	_, errCorrupt := corrupt.Query(ctx)
	respDup, errDup := dup.Query(ctx)
	check("假分片: 损坏/重复返回可注入", errCorrupt == nil && errDup == nil && respDup.Claimed == 2)

	if fail > 0 {
		fmt.Printf("TOTAL: %d OK, %d FAIL\n", pass, fail)
		return
	}
	fmt.Printf("TOTAL: %d OK, %d FAIL\n", pass, fail)
}
