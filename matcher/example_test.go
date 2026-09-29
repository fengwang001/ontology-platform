package matcher_test

import (
	"context"
	"log"
	"os"

	"ontology/graph"
	"ontology/matcher"
)

func Example() {
	g := graph.New()
	_ = g.AddObject(graph.Object{ID: "alice", Type: "Person",
		Attributes: map[string]any{"age": 30}})
	_ = g.AddObject(graph.Object{ID: "bob", Type: "Person",
		Attributes: map[string]any{"age": 25}})
	_ = g.AddObject(graph.Object{ID: "acme", Type: "Company"})
	_ = g.AddLink(graph.Link{Type: "worksAt", Source: "alice", Target: "acme"})
	_ = g.AddLink(graph.Link{Type: "worksAt", Source: "bob", Target: "acme"})

	logger := log.New(os.Stdout, "", 0)
	m := matcher.New(matcher.WithLogger(logger))

	p := &matcher.Pattern{
		Nodes: []matcher.NodeVar{
			{
				Name:        "senior",
				Type:        "Person",
				Constraints: []matcher.Constraint{{Attr: "age", Op: matcher.OpGt, Value: 28}},
			},
			{Name: "employer", Type: "Company"},
		},
		Edges: []matcher.EdgePat{
			{Type: "worksAt", Source: "senior", Target: "employer"},
		},
	}

	matches, _, err := m.Match(context.Background(), g.Snapshot(), p)
	if err != nil {
		log.Fatal(err)
	}
	for _, match := range matches {
		logger.Printf("result: senior=%s works at %s", match["senior"], match["employer"])
	}

	// Output:
	// match START pattern={nodes=[employer:Company, senior:Person{age > 28}] edges=[senior -worksAt-> employer]} candidates=map[employer:1 senior:1] order=[employer senior]
	// match ACCEPT binding={employer=acme, senior=alice} reason=node-types+constraints+all-edges-satisfied canonical=employer=acme|senior=alice
	// match DONE  pattern={nodes=[employer:Company, senior:Person{age > 28}] edges=[senior -worksAt-> employer]} accepted=1 embeddings=1 isomorphic-duplicates=0 pruned-constraint=1 pruned-edge=0 backtracks=0
	// result: senior=alice works at acme
}
