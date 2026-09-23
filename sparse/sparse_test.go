package sparse

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"ontology/segment"
)

func buildSegWithIndex(t *testing.T, dir string, firstSeq uint64, events, n int) (string, string) {
	t.Helper()
	segPath := filepath.Join(dir, "seg.dat")
	idxPath := filepath.Join(dir, "seg.idx")
	w, err := segment.Create(segPath, firstSeq)
	if err != nil {
		t.Fatal(err)
	}
	b := NewBuilder(n)
	w.OnEvent = b.Observe
	for i := 0; i < events; i++ {
		if _, err := w.Append([]byte{byte(i), byte(i >> 8)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.Index().Save(idxPath); err != nil {
		t.Fatal(err)
	}
	return segPath, idxPath
}

func TestLocate(t *testing.T) {
	dir := t.TempDir()
	_, idxPath := buildSegWithIndex(t, dir, 10, 10, 4) // anchors at seq 10,14,18
	idx, err := Load(idxPath)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		from    uint64
		wantSeq uint64
		wantOK  bool
	}{
		{"from equals anchor", 14, 14, true},
		{"from between anchors", 16, 14, true},
		{"from below first anchor", 5, 0, false},
		{"from at first anchor", 10, 10, true},
		{"from beyond last anchor", 99, 18, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, ok := idx.Locate(tc.from)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && a.Seq != tc.wantSeq {
				t.Fatalf("anchor seq = %d, want %d", a.Seq, tc.wantSeq)
			}
			if ok && a.Seq > tc.from {
				t.Fatalf("anchor %d is after from %d: would lose events", a.Seq, tc.from)
			}
		})
	}
}

func TestRebuildByteIdentical(t *testing.T) {
	dir := t.TempDir()
	segPath, idxPath := buildSegWithIndex(t, dir, 0, 1000, 128)
	original, err := os.ReadFile(idxPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(idxPath); err != nil {
		t.Fatal(err)
	}
	idx, err := Rebuild(segPath, 128)
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Save(idxPath); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := os.ReadFile(idxPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, rebuilt) {
		t.Fatalf("rebuilt index differs: %d vs %d bytes", len(original), len(rebuilt))
	}
}
