package main

import (
	"context"
	"fmt"
	"time"

	"ontology/fanout"
	"ontology/shard"
)

func main() {
	failed := 0
	checks := 0
	check := func(name string, ok bool) {
		checks++
		if ok {
			fmt.Printf("OK %s\n", name)
			return
		}
		failed++
		fmt.Printf("FAIL %s\n", name)
	}
	dups := shard.NewFake("", nil, 0).WithDuplicate()
	frames, _ := dups.Query(context.Background())
	bad := shard.NewFake("", nil, 0).WithCorrupt(1)
	corrupt, _ := bad.Query(context.Background())
	check("shard duplicate/corrupt injection is observable",
		len(frames) == 2 && corrupt[0].Declared != len(corrupt[0].Records))

	shards := make([]shard.Shard, 200)
	for i := range shards {
		shards[i] = shard.NewFake(fmt.Sprintf("s%03d", i), nil, 0).WithDelay(time.Millisecond)
	}
	runner, err := fanout.New(shards, 8)
	_ = runner.Run(context.Background())
	check("fanout concurrency peak never exceeds limit", err == nil && runner.PeakInFlight() > 0 && runner.PeakInFlight() <= 8)

	blocked, _ := fanout.New([]shard.Shard{shard.NewFake("z", nil, 0).WithHang()}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	blockedAttempts := blocked.Run(ctx)
	check("fanout launches nothing after deadline", len(blockedAttempts) == 1 && !blockedAttempts[0].Launched)

	if failed != 0 {
		fmt.Printf("TOTAL: FAIL %d/%d\n", checks-failed, checks)
		return
	}
	fmt.Printf("TOTAL: OK %d/%d\n", checks, checks)
}
