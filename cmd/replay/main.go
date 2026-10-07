// Command replay verifies a decision journal emitted by the ontology store.
//
// Usage:
//
//	go run ./cmd/replay -journal decisions.ndjson
//
// The schema is read from -schema (an optional JSON file mapping object and
// link types); by default the built-in test schema is used, which matches
// the schema used by the randomised differential tests.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"ontology/ontology"
)

func main() {
	journalPath := flag.String("journal", "", "path to newline-delimited decision journal (required)")
	schemaPath := flag.String("schema", "", "optional JSON schema file ({\"object_types\":{...},\"link_types\":{...}})")
	flag.Parse()

	if *journalPath == "" {
		fmt.Fprintln(os.Stderr, "missing -journal")
		os.Exit(2)
	}
	f, err := os.Open(*journalPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open journal: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	cfg, err := loadSchema(*schemaPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load schema: %v\n", err)
		os.Exit(1)
	}

	report, err := ontology.ReplayJournal(cfg, f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "replay failed: %v\n", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		fmt.Fprintf(os.Stderr, "encode report: %v\n", err)
		os.Exit(1)
	}
}

func loadSchema(path string) (ontology.Config, error) {
	if path == "" {
		return defaultSchema(), nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ontology.Config{}, err
	}
	var schema struct {
		ObjectTypes map[string]ontology.ObjectType `json:"object_types"`
		LinkTypes   map[string]ontology.LinkType   `json:"link_types"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		return ontology.Config{}, err
	}
	return ontology.Config{ObjectTypes: schema.ObjectTypes, LinkTypes: schema.LinkTypes}, nil
}

func defaultSchema() ontology.Config {
	return ontology.Config{
		ObjectTypes: map[string]ontology.ObjectType{
			"Person": {Name: "Person"},
			"Org":    {Name: "Org"},
		},
		LinkTypes: map[string]ontology.LinkType{
			"member": {
				Name:       "member",
				SourceType: "Person",
				TargetType: "Org",
				MaxSource:  2,
				MaxTarget:  -1,
			},
		},
	}
}
