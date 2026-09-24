package sparse

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"ontology/segment"
)

func makeSegment(t *testing.T, dir string, firstSeq, count uint64) string {
	t.Helper()
	w, err := segment.Create(dir, firstSeq)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := uint64(0); i < count; i++ {
		if err := w.Append([]byte(fmt.Sprintf("p-%d", i))); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return filepath.Join(dir, segment.Name(firstSeq))
}

func TestFloor(t *testing.T) {
	segPath := makeSegment(t, t.TempDir(), 50, 40)
	idx, err := Build(segPath, 4)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	cases := []struct {
	name   string
	from   uint64
	found  bool
	wantSeq uint64
	}{
		{"below first anchor", 5, false, 0},
		{"equal to first anchor", 50, true, 50},
		{"equal to middle anchor", 58, true, 58},
		{"between anchors", 61, true, 58},
		{"past last anchor", 1000, true, 86},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, ok := idx.Floor(tc.from)
			if ok != tc.found {
				t.Fatalf("found=%v, want %v", ok, tc.found)
			}
			if ok && a.Seq != tc.wantSeq {
				t.Fatalf("anchor seq=%d, want %d", a.Seq, tc.wantSeq)
			}
		})
	}
}

func TestSaveLoadAndByteIdenticalRebuild(t *testing.T) {
	dir := t.TempDir()
	segPath := makeSegment(t, dir, 7, 250)
	idxPath := IndexPath(segPath)
	idx, err := Build(segPath, 16)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := idx.Save(idxPath); err != nil {
		t.Fatalf("save: %v", err)
	}
	orig, err := os.ReadFile(idxPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	loaded, err := Load(idxPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Interval != idx.Interval || len(loaded.Anchors) != len(idx.Anchors) {
		t.Fatalf("loaded index differs: %+v vs %+v", loaded, idx)
	}
	for i := range idx.Anchors {
		if loaded.Anchors[i] != idx.Anchors[i] {
			t.Fatalf("anchor %d: %+v vs %+v", i, loaded.Anchors[i], idx.Anchors[i])
		}
	}
	if err := os.Remove(idxPath); err != nil {
		t.Fatalf("remove: %v", err)
	}
	again, err := Build(segPath, 16)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if err := again.Save(idxPath); err != nil {
		t.Fatalf("resave: %v", err)
	}
	rebuilt, err := os.ReadFile(idxPath)
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	if !bytes.Equal(orig, rebuilt) {
		t.Fatalf("rebuilt index is not byte-identical (%d vs %d bytes)", len(orig), len(rebuilt))
	}
}
