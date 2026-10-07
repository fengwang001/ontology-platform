// Command demo exercises the temporal traversal subsystem end to end: it
// builds history, pins a baseline, then keeps mutating the graph/schema and
// re-traverses the old baseline to show the frozen, migration- and
// cardinality-independent result.
package main

import (
	"fmt"

	"ontology/temporal"
)

func main() {
	s := temporal.NewStore()

	must := func(at temporal.Instant, err error) temporal.Instant {
		if err != nil {
			panic(err)
		}
		return at
	}

	// Schema.
	tx := s.Begin()
	tx.CreateObjectType("Person", []temporal.Property{
		{Name: "name", Type: "string"},
		{Name: "age", Type: "int"},
	})
	tx.CreateLinkType("knows", temporal.Cardinality{MaxOut: -1})
	must(tx.Commit())

	// Data at instants 2..3.
	tx = s.Begin()
	tx.CreateObject("alice", "Person", temporal.PropertyValues{"name": "Alice", "age": 30})
	tx.CreateObject("bob", "Person", temporal.PropertyValues{"name": "Bob", "age": 41})
	must(tx.Commit())

	tx = s.Begin()
	if err := tx.CreateLink("knows", "alice", "bob"); err != nil {
		panic(err)
	}
	baseline := must(tx.Commit())

	audit := &temporal.MemoryAudit{}
	show := func(label string) {
		res, err := s.Traverse(temporal.TraversalConfig{
			Start:  "alice",
			At:     baseline,
			Limits: temporal.TraversalLimits{MaxDepth: -1, MaxVisited: -1},
		}, audit)
		if err != nil {
			panic(err)
		}
		fmt.Printf("%s (baseline=%d, current head=%d):\n", label, baseline, s.Head())
		for _, o := range res.Objects {
			fmt.Printf("  obj d=%d id=%s type=%s props=%v\n",
				o.Depth, o.State.ID, o.State.Type, o.State.Properties)
		}
		for _, l := range res.Links {
			fmt.Printf("  link %s:%s->%s\n", l.Type, l.Src, l.Dst)
		}
	}

	show("before later changes")

	// Later: schema migration and cardinality tightening, new objects/links.
	tx = s.Begin()
	tx.MigrateObjectType("Person", []temporal.Property{
		{Name: "name", Type: "string"},
		{Name: "score", Type: "int"},
	})
	must(tx.Commit())

	tx = s.Begin()
	tx.AdjustCardinality("knows", temporal.Cardinality{MaxOut: 1})
	tx.CreateObject("carol", "Person", temporal.PropertyValues{"name": "Carol", "score": 9})
	must(tx.Commit())

	tx = s.Begin()
	if err := tx.CreateLink("knows", "bob", "carol"); err != nil {
		panic(err)
	}
	must(tx.Commit())

	show("after migration + cardinality change + new link")
	fmt.Printf("audit records emitted: %d\n", len(audit.Records))
}
