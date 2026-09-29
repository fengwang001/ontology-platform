// replay-demo 演示基于写集依赖的并行回放调度器。
package main

import (
	"context"
	"fmt"
	"os"

	"ontology/ontology/scheduler"
)

func main() {
	s := scheduler.New()

	// 写集决定依赖；读集只做审计。
	txs := []scheduler.Transaction{
		{Seq: 1, ReadKeys: []string{"a"}, WriteKeys: []string{"a", "b"}, WriteValues: map[string]string{"a": "1"}},
		{Seq: 2, ReadKeys: []string{"a"}, WriteKeys: []string{"c"}, WriteValues: map[string]string{"c": "2"}},
		{Seq: 3, ReadKeys: []string{"b", "c"}, WriteKeys: []string{"b", "c"}},
		{Seq: 4, WriteKeys: []string{"d"}, WriteValues: map[string]string{"d": "4"}},
		{Seq: 5, WriteKeys: []string{"a", "d"}},
	}
	for _, tx := range txs {
		if err := s.Add(tx); err != nil {
			fmt.Fprintf(os.Stderr, "add tx %d failed: %v\n", tx.Seq, err)
			os.Exit(1)
		}
	}

	const maxParallel = 2

	fmt.Println("== audit log ==")
	if err := s.DumpAuditLog(os.Stdout, maxParallel); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	rounds, err := s.Schedule(maxParallel)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("== rounds ==")
	for _, r := range rounds {
		fmt.Printf("round %d (parallel=%v): %v\n", r.Round, r.Parallel, r.TxSeqs)
	}

	res, err := s.Replay(context.Background(), maxParallel, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("== final state ==")
	for k, v := range res.State {
		fmt.Printf("%s=%s\n", k, v)
	}

	if err := s.SelfCheck(); err != nil {
		fmt.Fprintln(os.Stderr, "self check failed:", err)
		os.Exit(1)
	}
}
