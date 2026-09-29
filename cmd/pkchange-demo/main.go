// 主键变更拆分与分区投递的最小可运行示例：
//
//	go run ./cmd/pkchange-demo
package main

import (
	"fmt"
	"log/slog"
	"os"

	"ontology/pkchange"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	store := pkchange.NewStore(4, 100, logger)

	batches := [][]pkchange.Change{
		{
			{Op: pkchange.OpInsert, Key: "user-1", Data: pkchange.Row{"name": "alice"}},
			{Op: pkchange.OpInsert, Key: "user-2", Data: pkchange.Row{"name": "bob"}},
		},
		{
			// 主键不变的更新：仅一次写入。
			{Op: pkchange.OpUpdate, Key: "user-2", Data: pkchange.Row{"name": "bobby"}},
			// 主键变化的更新：先删旧键，再写新键。
			{Op: pkchange.OpUpdate, Key: "user-1", NewKey: "user-100", Data: pkchange.Row{"name": "alice"}},
		},
		{
			// 非法批：插入已存在的键，整批被拒绝。
			{Op: pkchange.OpInsert, Key: "user-100", Data: pkchange.Row{"name": "dup"}},
		},
	}

	for i, batch := range batches {
		fmt.Printf("\n===== batch %d =====\n", i+1)
		parts, err := store.Apply(batch)
		if err != nil {
			if rj, ok := pkchange.AsReject(err); ok {
				fmt.Printf("rejected: reason=%s index=%d key=%q\n", rj.Reason, rj.Index, rj.Key)
			} else {
				fmt.Printf("error: %v\n", err)
			}
			continue
		}
		for _, p := range parts {
			if len(p.Events) == 0 {
				continue
			}
			fmt.Printf("partition %d: ", p.Index)
			for _, ev := range p.Events {
				fmt.Printf("[%s %s] ", ev.Kind, ev.Key)
			}
			fmt.Println()
		}
	}

	source, view := store.Snapshots()
	fmt.Printf("\nfinal source: %v\nfinal view:   %v\n", source, view)
}
