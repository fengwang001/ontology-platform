package sparse

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"ontology/event"
	"ontology/segment"
)

func TestLookupPositions(t *testing.T) {
	x := &Index{Every: 128, Anchors: []Anchor{
		{Seq: 128, Offset: 10000},
		{Seq: 256, Offset: 20000},
	}}
	cases := []struct {
		name    string
		from    uint64
		wantSeq uint64
		found   bool
	}{
		{"at anchor", 128, 128, true},
		{"between anchors", 200, 128, true},
		{"before first anchor", 50, 0, false},
		{"after last anchor", 999, 256, true},
		{"exactly zero", 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, ok := x.Lookup(tc.from)
			if ok != tc.found || (ok && a.Seq != tc.wantSeq) {
				t.Fatalf("Lookup(%d) = %+v,%v want seq=%d found=%v", tc.from, a, ok, tc.wantSeq, tc.found)
			}
		})
	}
}

func TestRebuildByteIdentical(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "seg-0001")
	const n, every = 1000, uint64(128)
	w, err := segment.Create(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if err := w.Append(event.Event{Seq: uint64(i), Payload: []byte{byte(i), byte(i >> 8)}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	idxPath := path + ".idx"
	writeRebuilt(t, path, idxPath, every)
	origBytes, err := os.ReadFile(idxPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(idxPath); err != nil {
		t.Fatal(err)
	}
	rebuilt := writeRebuilt(t, path, idxPath, every)
	rebBytes, err := os.ReadFile(idxPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(origBytes, rebBytes) {
		t.Fatal("rebuilt index differs from original")
	}
	if len(rebuilt.Anchors) != (n-1)/int(every) {
		t.Fatalf("anchors = %d", len(rebuilt.Anchors))
	}
	want := every
	for _, a := range rebuilt.Anchors {
		if a.Seq != want {
			t.Fatalf("anchor seq = %d want %d", a.Seq, want)
		}
		if a.Offset < segment.HeaderSize {
			t.Fatalf("bad offset %d", a.Offset)
		}
		want += every
	}
}

func writeRebuilt(t *testing.T, segPath, idxPath string, every uint64) *Index {
	t.Helper()
	r, err := segment.Open(segPath)
	if err != nil {
		t.Fatal(err)
	}
	x, err := FromSegment(r, every)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := x.WriteFile(idxPath); err != nil {
		t.Fatal(err)
	}
	return x
}
