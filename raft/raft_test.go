package raft

import (
	"fmt"
	"testing"
)

// TestIncrementalCommitCounter is the white-box proof: after m committed
// current-term entries, adding one more and re-checking CommitIndex must
// inspect exactly one entry, independent of m. The counter is read here
// (same package) and nowhere in any exported interface.
func TestIncrementalCommitCounter(t *testing.T) {
	cases := []int{100, 1000, 10000}
	for _, m := range cases {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			c := NewCluster("S1", "S2", "S3")
			if err := c.SetTerm("S1", 1); err != nil {
				t.Fatal(err)
			}
			for range m {
				if _, err := c.Append("S1", "c"); err != nil {
					t.Fatal(err)
				}
			}
			if err := c.Replicate("S1", "S2", 0); err != nil {
				t.Fatal(err)
			}
			ci, err := c.CommitIndex("S1")
			if err != nil || ci != m {
				t.Fatalf("baseline CommitIndex = %d, %v; want %d", ci, err, m)
			}
			if _, err := c.Append("S1", "c"); err != nil {
				t.Fatal(err)
			}
			if err := c.Replicate("S1", "S2", m); err != nil {
				t.Fatal(err)
			}
			ci, err = c.CommitIndex("S1")
			if err != nil {
				t.Fatal(err)
			}
			if ci != m+1 {
				t.Fatalf("CommitIndex = %d, want %d", ci, m+1)
			}
			// The whole point: a full rescan would inspect m+1 entries.
			if c.scanned != 1 {
				t.Fatalf("scanned = %d, want 1 (incremental, m-independent)", c.scanned)
			}
		})
	}
}

// TestSkippedOldTermCountsScan pins resume semantics: an old-term index
// between the previous point and the new current-term entry is inspected
// and skipped, never committed directly.
func TestSkippedOldTermCountsScan(t *testing.T) {
	c := NewCluster("S1", "S2", "S3")
	mustSet := func(s string, term int) {
		if err := c.SetTerm(s, term); err != nil {
			t.Fatal(err)
		}
	}
	mustSet("S1", 1)
	if _, err := c.Append("S1", "a"); err != nil {
		t.Fatal(err)
	}
	if err := c.Replicate("S1", "S2", 0); err != nil {
		t.Fatal(err)
	}
	if ci, _ := c.CommitIndex("S1"); ci != 1 {
		t.Fatalf("ci after step1 = %d, want 1", ci)
	}
	if _, err := c.Append("S1", "b"); err != nil {
		t.Fatal(err)
	}
	if err := c.Replicate("S1", "S2", 1); err != nil {
		t.Fatal(err)
	}
	mustSet("S1", 3)
	if _, err := c.Append("S1", "z"); err != nil {
		t.Fatal(err)
	}
	if err := c.Replicate("S1", "S2", 2); err != nil {
		t.Fatal(err)
	}
	ci, _ := c.CommitIndex("S1")
	if ci != 3 {
		t.Fatalf("CommitIndex = %d, want 3 (z commits, old b rides along)", ci)
	}
	if c.scanned != 2 {
		t.Fatalf("scanned = %d, want 2 (index 2 skipped, index 3 committed)", c.scanned)
	}
}
