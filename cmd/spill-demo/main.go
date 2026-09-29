// Command spill-demo walks through spill selection, ordered commit replay and
// rollback cleanup, printing every input step, the in-memory row count and the
// decision rationale.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"ontology/spill"
)

type stepLogger struct{}

func (stepLogger) Printf(format string, args ...any) {
	log.Printf("  "+format, args...)
}

func must(err error) {
	if err != nil {
		log.Fatalf("unexpected error: %v", err)
	}
}

func main() {
	log.SetOutput(os.Stdout)
	log.SetFlags(0)

	fmt.Println("== spill demo: MaxMemoryRows=4, MaxSpillBlocks=10 ==")
	mgr, err := spill.New(spill.Config{
		MaxMemoryRows:  4,
		MaxSpillBlocks: 10,
		Log:            stepLogger{},
	})
	must(err)

	must(mgr.Begin(1))
	must(mgr.Begin(2))
	must(mgr.Begin(3))

	must(mgr.Append(1, "t1-a", "t1-b"))
	must(mgr.Append(2, "t2-a", "t2-b", "t2-c"))
	must(mgr.Append(3, "t3-a"))

	fmt.Println("-- txn 1 grows to 3 rows, tying txn 2; more pressure follows --")
	must(mgr.Append(1, "t1-c"))
	must(mgr.Append(2, "t2-d", "t2-e"))

	fmt.Printf("live spill blocks: %d\n", len(mgr.QueryBlocks()))

	fmt.Println("-- commit txn 1: blocks replayed ascending, then memory rows --")
	must(mgr.Commit(context.Background(), 1, func(r spill.Row) error {
		fmt.Printf("  downstream <- %q\n", r)
		return nil
	}))

	fmt.Println("-- rollback txn 2: blocks freed, nothing emitted --")
	must(mgr.Rollback(2))

	fmt.Println("-- commit txn 3 --")
	must(mgr.Commit(context.Background(), 3, func(r spill.Row) error {
		fmt.Printf("  downstream <- %q\n", r)
		return nil
	}))

	fmt.Printf("final committed log: %v\n", mgr.LogSnapshot())
	fmt.Printf("leftover blocks=%d openTxns=%d\n", mgr.BlockCount(), len(mgr.QueryTxns()))

	fmt.Println("-- rejection demo: duplicate txn id --")
	must(mgr.Begin(9))
	if err := mgr.Begin(9); err != nil {
		fmt.Printf("rejected as expected: %v\n", err)
	}
}
