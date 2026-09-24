package dump_test

import (
	"bytes"
	"errors"
	"testing"

	"ontology/dump"
	"ontology/stack"
	"ontology/tree"
)

// fixture: 4 samples over 5 nodes (root, a, b, c, d).
// Record offsets: root@32 a@59 b@87 c@115 d@143, records end at 171,
// CRC at 171..175, total file size 175.
func fixture(t *testing.T) []byte {
	t.Helper()
	tr := tree.New()
	for _, frames := range [][]string{
		{"a", "b", "c"}, {"a", "b", "c"}, {"a", "b"}, {"a", "d"},
	} {
		s, err := stack.Normalize(frames, 64)
		if err != nil {
			t.Fatal(err)
		}
		tr.Insert(s)
	}
	buf := new(bytes.Buffer)
	if err := dump.Write(buf, tr.Snapshot()); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func countNodes(n *tree.SNode) int {
	total := 1
	for _, c := range n.Children {
		total += countNodes(c)
	}
	return total
}

func TestRoundTrip(t *testing.T) {
	data := fixture(t)
	snap, err := dump.Read(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := countNodes(snap.Root); got != 5 {
		t.Fatalf("nodes=%d want 5", got)
	}
	if snap.SelfSum() != 4 || snap.Samples != 4 {
		t.Fatalf("self-sum=%d samples=%d, want 4", snap.SelfSum(), snap.Samples)
	}
	if snap.TotalSum() != 4+4+3+2+1 {
		t.Fatalf("total-sum=%d want 14", snap.TotalSum())
	}
}

func TestTruncation(t *testing.T) {
	data := fixture(t)
	const headerSize, crcSize = 32, 4
	recordsEnd := len(data) - crcSize
	// Recovered node counts at record boundaries, synthetic root included.
	wantNodes := map[int]int{32: 1, 59: 1, 87: 2, 115: 3, 143: 4, 171: 5}
	for i := 1; i < len(data); i++ {
		snap, err := dump.Recover(data[:i])
		if err == nil {
			t.Fatalf("trunc=%d: expected error", i)
		}
		var wantErr error
		switch {
		case i < headerSize:
			wantErr = dump.ErrHeaderIncomplete
		case i < recordsEnd:
			wantErr = dump.ErrRecordIncomplete
		default:
			wantErr = dump.ErrCRCMismatch
		}
		if !errors.Is(err, wantErr) {
			t.Fatalf("trunc=%d: err=%v want class %v", i, err, wantErr)
		}
		// Maximal recoverable prefix must be self-consistent.
		if snap.SelfSum() != snap.Samples {
			t.Fatalf("trunc=%d: self-sum %d != recovered samples %d", i, snap.SelfSum(), snap.Samples)
		}
		var checkOrphans func(n *tree.SNode)
		checkOrphans = func(n *tree.SNode) {
			for _, c := range n.Children {
				if c.Parent != n {
					t.Fatalf("trunc=%d: orphan node %q", i, c.Frame)
				}
				checkOrphans(c)
			}
		}
		checkOrphans(snap.Root)
		if want, ok := wantNodes[i]; ok {
			if got := countNodes(snap.Root); got != want {
				t.Fatalf("trunc=%d: recovered %d nodes want %d", i, got, want)
			}
		}
	}
	// The three classes are mutually distinguishable.
	classes := []error{dump.ErrHeaderIncomplete, dump.ErrRecordIncomplete, dump.ErrCRCMismatch}
	for _, a := range classes {
		for _, b := range classes {
			if a != b && errors.Is(a, b) {
				t.Fatalf("%v and %v not distinguishable", a, b)
			}
		}
	}
}

func TestTruncatedFlagSurvives(t *testing.T) {
	tr := tree.New()
	s, err := stack.Normalize([]string{"a", "b", "c", "d"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	tr.Insert(s)
	buf := new(bytes.Buffer)
	if err := dump.Write(buf, tr.Snapshot()); err != nil {
		t.Fatal(err)
	}
	snap, err := dump.Read(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	leaf := snap.Root.Children[0].Children[0]
	if !leaf.Truncated || snap.TruncatedSamples != 1 {
		t.Fatalf("truncated marker lost: %+v samples=%d", leaf, snap.TruncatedSamples)
	}
}
