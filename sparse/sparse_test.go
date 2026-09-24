package sparse

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"ontology/event"
	"ontology/segment"
)

func buildSeg(t *testing.T, dir string, first, n uint64) string {
	t.Helper()
	p := filepath.Join(dir, fmt.Sprintf("%020d.seg", first))
	w, err := segment.Create(p, first)
	if err != nil {
		t.Fatal(err)
	}
	for i := uint64(0); i < n; i++ {
		if err := w.Append(event.Event{Seq: first + i, Payload: []byte("x")}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLocateRule(t *testing.T) {
	dir := t.TempDir()
	seg := buildSeg(t, dir, 100, 100) // seq 100..199, N=10
	idxPath := seg + ".idx"
	if err := Build(seg, idxPath, 10); err != nil {
		t.Fatal(err)
	}
	idx, err := Load(idxPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(idx.Anchors); got != 10 {
		t.Fatalf("anchors=%d want 10", got)
	}
	cases := []struct {
		name    string
		from    uint64
		wantSeq uint64
		wantOK  bool
	}{
		{"from equals anchor", 150, 150, true},
		{"from between anchors", 157, 150, true},
		{"from below first anchor", 50, 0, false},
		{"from above last anchor", 199, 190, true},
		{"from beyond segment", 99999, 190, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, ok := idx.Locate(tc.from)
			if ok != tc.wantOK || (ok && a.Seq != tc.wantSeq) {
				t.Fatalf("Locate(%d)=(%v,%v), want seq=%d ok=%v",
					tc.from, a, ok, tc.wantSeq, tc.wantOK)
			}
			if ok && a.Seq > tc.from {
				t.Fatalf("anchor seq %d > from %d: would lose events", a.Seq, tc.from)
			}
		})
	}
}

func TestRebuildByteIdentical(t *testing.T) {
	dir := t.TempDir()
	seg := buildSeg(t, dir, 0, 500)
	idxPath := seg + ".idx"
	if err := Build(seg, idxPath, 7); err != nil {
		t.Fatal(err)
	}
	orig, err := os.ReadFile(idxPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(idxPath); err != nil {
		t.Fatal(err)
	}
	if err := Build(seg, idxPath, 7); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := os.ReadFile(idxPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(orig, rebuilt) {
		t.Fatal("rebuilt index differs from original")
	}
}
