// Command demo exercises the atomic batch materializer and prints inputs,
// per-event rehearsal reasoning and final results.
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/ontology"
)

func strptr(s string) *string { return &s }

func main() {
	m := ontology.New(ontology.WithLogger(os.Stdout))

	run := func(name string, batch []ontology.Event) {
		fmt.Printf("\n=== %s ===\ninput: %v\n", name, batch)
		err := m.Apply(batch)
		if err != nil {
			var be *ontology.BatchError
			if errors.As(err, &be) {
				fmt.Printf("result: REJECTED at event %d key=%q reason=%v\n",
					be.Index, be.Key, be.Reason)
			} else {
				fmt.Printf("result: REJECTED reason=%v\n", err)
			}
			return
		}
		fmt.Printf("result: COMMITTED snapshot=%v\n", m.Snapshot())
	}

	run("batch 1: write x",
		[]ontology.Event{{Key: "x", Op: ontology.Write, Value: "1", Expect: nil}})

	run("batch 2: ordered write-then-delete on same key, plus chained expect",
		[]ontology.Event{
			{Key: "x", Op: ontology.Write, Value: "2", Expect: strptr("1")},
			{Key: "x", Op: ontology.Delete, Expect: strptr("2")},
			{Key: "y", Op: ontology.Write, Value: "a", Expect: nil},
			{Key: "y", Op: ontology.Write, Value: "b", Expect: strptr("a")},
		})

	run("batch 3: first failing event rejects the whole batch all-or-nothing",
		[]ontology.Event{
			{Key: "z", Op: ontology.Write, Value: "z1", Expect: nil},
			{Key: "y", Op: ontology.Delete, Expect: strptr("STALE")},
			{Key: "z", Op: ontology.Delete, Expect: strptr("z1")},
		})

	run("batch 4: corrected batch commits; z is still absent so it writes",
		[]ontology.Event{{Key: "z", Op: ontology.Write, Value: "z1", Expect: nil}})

	fmt.Printf("\nfinal view=%v generation=%d check=%v\n",
		m.Snapshot(), m.Generation(), m.Check())
}
