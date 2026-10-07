package ontology

import (
	"os"
	"strings"
	"testing"
)

func TestJournalRecordsAllAttemptsAndReplays(t *testing.T) {
	path := "/tmp/ontology-journal-test.jsonl"
	os.Remove(path)
	j := NewJournal(path)
	p := testPlatform()
	p.SetJournal(j)

	p.Commit(Batch{ID: "ok", Ops: []Operation{
		{Instance: "a", Type: "Person", BaseVersion: 0, Props: Properties{"name": "A"}},
	}})
	p.Commit(Batch{ID: "dup", Ops: []Operation{
		{Instance: "z", Type: "Person", BaseVersion: 0, Props: Properties{"name": "Z"}},
		{Instance: "z", Type: "Person", BaseVersion: 0, Props: Properties{"name": "Z2"}},
	}})
	p.Commit(Batch{ID: "conflict", Ops: []Operation{
		{Instance: "a", Type: "Person", BaseVersion: 9, Props: Properties{"name": "A2"}},
	}})
	p.Commit(Batch{ID: "hookbad", Ops: []Operation{
		{Instance: "a", Type: "Person", BaseVersion: 1, Props: Properties{"other": 1}},
	}})

	entries := j.Entries()
	classes := map[string]bool{}
	for _, e := range entries {
		if e.Failure != "" {
			classes[e.Failure] = true
		}
		if !e.OK && e.Tick != 0 {
			t.Fatalf("failed entry %s has nonzero tick %d", e.BatchID, e.Tick)
		}
	}
	for _, want := range []string{"duplicate_write", "version_conflict", "hook_rejected"} {
		if !classes[want] {
			t.Fatalf("journal missing failure class %s: %v", want, classes)
		}
	}
	if len(entries) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(entries))
	}

	nm := emptyNaive(p)
	var committed int
	for _, e := range entries {
		nr := nm.apply(recordToBatch(e.Batch))
		if nr.ok != e.OK {
			t.Fatalf("replay mismatch on %s: naive=%v logged=%v", e.BatchID, nr.ok, e.OK)
		}
		if nr.ok {
			committed++
		}
	}
	if committed != 1 || nm.tick != 1 {
		t.Fatalf("replay: committed=%d tick=%d", committed, nm.tick)
	}

	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "hookbad") {
		t.Fatalf("journal file missing or incomplete: %v", err)
	}
}
