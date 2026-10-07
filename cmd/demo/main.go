// Command demo exercises the joint batch store and writes its decision
// journal to stdout or -out, producing an artifact replay can verify.
package main

import (
	"flag"
	"os"

	"ontology/ontology"
)

func main() {
	outPath := flag.String("out", "", "journal output path (default stdout)")
	flag.Parse()

	cfg := ontology.Config{
		ObjectTypes: map[string]ontology.ObjectType{
			"Person": {Name: "Person"},
			"Org":    {Name: "Org"},
		},
		LinkTypes: map[string]ontology.LinkType{
			"member": {
				Name: "member", SourceType: "Person", TargetType: "Org",
				MaxSource: 2, MaxTarget: -1,
			},
		},
	}
	s := ontology.New(cfg)
	must(s.CreateInstance("alice", "Person"))
	must(s.CreateInstance("bob", "Person"))
	must(s.CreateInstance("acme", "Org"))

	s.Commit(ontology.Batch{
		ID: "joint-ok",
		Preconditions: []ontology.Precondition{
			{Instance: "alice", ExpectedVersion: 1},
			{Instance: "acme", ExpectedVersion: 1},
		},
		Ops: []ontology.Op{
			{Kind: ontology.OpSetAttr, Instance: "alice", Attr: "name", Value: "Alice"},
			{Kind: ontology.OpAddLink, Instance: "alice", LinkType: "member", Other: "acme"},
		},
	})
	s.Commit(ontology.Batch{
		ID: "duplicate",
		Preconditions: []ontology.Precondition{
			{Instance: "alice", ExpectedVersion: 2},
			{Instance: "alice", ExpectedVersion: 2},
		},
	})
	s.Commit(ontology.Batch{
		ID:            "stale",
		Preconditions: []ontology.Precondition{{Instance: "alice", ExpectedVersion: 1}},
	})

	w := os.Stdout
	if *outPath != "" {
		f, err := os.Create(*outPath)
		if err != nil {
			panic(err)
		}
		defer f.Close()
		w = f
	}
	if _, err := s.Journal().WriteTo(w); err != nil {
		panic(err)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
