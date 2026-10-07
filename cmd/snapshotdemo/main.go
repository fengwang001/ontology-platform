// Command snapshotdemo writes an ontology export, applies one of several
// damage classes and shows the read-only verification decisions: block
// checksum, count consistency and conservative cross-block reference verdicts.
//
// Usage:
//
//	go run ./cmd/snapshotdemo -dir /tmp/ontodemo -damage none|flip|count|dangle|targetcount
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"ontology/snapshot"
)

func main() {
	dir := flag.String("dir", "/tmp/ontodemo", "export directory")
	damage := flag.String("damage", "none", "none|flip|count|dangle|targetcount")
	flag.Parse()

	if err := os.RemoveAll(*dir); err != nil {
		fail(err)
	}
	req := snapshot.ExportRequest{
		"person": {
			{ID: "p1", Props: map[string]string{"name": "Ada"}, Links: []snapshot.Link{
				{Field: "works_for", Target: "org", ID: "o1"},
			}},
			{ID: "p2", Props: map[string]string{"name": "Lin"}, Links: []snapshot.Link{
				{Field: "mentor", Target: "person", ID: "p1"},
			}},
		},
		"org": {
			{ID: "o1", Props: map[string]string{"name": "Eng"}, Links: []snapshot.Link{
				{Field: "lead", Target: "person", ID: "p1"},
			}},
		},
	}
	if err := snapshot.WriteExport(context.Background(), *dir, req); err != nil {
		fail(err)
	}

	switch *damage {
	case "none":
	case "flip":
		must(snapshot.FlipPayloadByte(*dir, "org", 4))
	case "count":
		must(snapshot.SetDeclaredCount(*dir, "person", 99))
	case "dangle":
		must(snapshot.RemoveTargetRecord(*dir, "org", "o1"))
	case "targetcount":
		must(snapshot.SetDeclaredCount(*dir, "org", 42))
	default:
		fail(fmt.Errorf("unknown damage %q", *damage))
	}

	logger := snapshot.NewDecisionLogger()
	loader := snapshot.NewLoader()
	report, err := loader.Verify(context.Background(), *dir, logger)
	if err != nil {
		fail(err)
	}

	fmt.Println("== decision log (inputs / output / basis) ==")
	for _, d := range logger.Decisions() {
		fmt.Printf("- %-8s input=%-28s output=%-17s basis=%s\n", d.Stage, d.Input, d.Output, d.Basis)
	}

	fmt.Println("\n== block results ==")
	for _, typ := range report.Types {
		br := report.Blocks[typ]
		fmt.Printf("- %-7s status=%-15s declared=%d actual=%d\n", typ, br.Status, br.Declared, br.Actual)
	}

	fmt.Println("\n== aggregate request ==")
	if _, err := loader.Aggregate(context.Background(), *dir, nil, snapshot.NewDecisionLogger()); err != nil {
		if le, ok := err.(*snapshot.LoadError); ok {
			for _, f := range le.Findings {
				fmt.Printf("- REJECT [%s] %s\n", f.Kind, f.Message)
			}
		} else {
			fail(err)
		}
	} else {
		fmt.Println("- aggregate view generated")
	}

	fmt.Println("\n== partial availability ==")
	for _, typ := range report.Types {
		if recs, ok := snapshot.ExtractBlock(report, typ); ok {
			fmt.Printf("- block %q independently usable, %d records\n", typ, len(recs))
		} else {
			fmt.Printf("- block %q not independently usable\n", typ)
		}
	}
}

func must(err error) {
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
