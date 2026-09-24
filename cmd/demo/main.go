package main

import (
	"context"
	"fmt"

	"ontology/shard"
)

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"shard fake returns claimed records", checkShard()},
	}
	pass := 0
	for _, c := range checks {
		if c.ok {
			pass++
			fmt.Printf("OK   %s\n", c.name)
		} else {
			fmt.Printf("FAIL %s\n", c.name)
		}
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
}

func checkShard() bool {
	f := shard.New(shard.Config{
		IDString: "s0",
		Records:  []shard.Record{{ID: "a", V: 1}},
		Upper:    5,
	})
	r := f.Query(context.Background())
	return f.ID() == "s0" && r.Claimed == 1 && len(r.Records) == 1 && r.Upper == 5
}
