package sparse

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"ontology/event"
	"ontology/segment"
)

func writeSeg(t *testing.T, dir string, every, first uint64, n int) string {
	t.Helper()
	path := filepath.Join(dir, "seg.log")
	w, err := segment.Create(path, every, first)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if err := w.Append(event.Encode(event.Event{Seq: first + uint64(i), Payload: []byte{byte(i)}})); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLocatePositions(t *testing.T) {
	// every=4, seqs 10..29, anchors at 10,14,18,22,26.
	idx := Index{Every: 4, Anchors: []Anchor{
		{Seq: 10, Offset: 28}, {Seq: 14, Offset: 100}, {Seq: 18, Offset: 200},
		{Seq: 22, Offset: 300}, {Seq: 26, Offset: 400},
	}}
	cases := []struct {
		name    string
		from    uint64
		wantSeq uint64
		wantOff int64
		ok      bool
	}{
		{"exactly on anchor", 18, 18, 200, true},
		{"between anchors", 20, 18, 200, true},
		{"just before next anchor", 21, 18, 200, true},
		{"below first anchor", 5, 0, 0, false},
		{"at first anchor", 10, 10, 28, true},
		{"past last anchor", 29, 26, 400, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, ok := idx.Locate(tc.from)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && (a.Seq != tc.wantSeq || a.Offset != tc.wantOff) {
				t.Fatalf("anchor = %+v, want seq=%d off=%d", a, tc.wantSeq, tc.wantOff)
			}
			if ok && a.Seq > tc.from {
				t.Fatalf("anchor seq %d > from %d", a.Seq, tc.from)
			}
		})
	}
}

func TestRebuildByteIdentical(t *testing.T) {
	dir := t.TempDir()
	segPath := writeSeg(t, dir, 8, 1000, 500)
	idx, err := Build(segPath)
	if err != nil {
		t.Fatal(err)
	}
	idxPath := filepath.Join(dir, "seg.idx")
	if err := WriteFile(idxPath, idx); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(idxPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(idxPath); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := Build(segPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(idxPath, rebuilt); err != nil {
		t.Fatal(err)
	}
	reloaded, err := os.ReadFile(idxPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, reloaded) {
		t.Fatalf("rebuilt index differs: %d vs %d bytes", len(original), len(reloaded))
	}
}

func TestAnchorOffsets(t *testing.T) {
	dir := t.TempDir()
	segPath := writeSeg(t, dir, 4, 0, 10)
	idx, err := Build(segPath)
	if err != nil {
		t.Fatal(err)
	}
	want := []Anchor{{0, 28}, {4, 96}, {8, 164}} // record size = 4+9+4 = 17
	if len(idx.Anchors) != len(want) {
		t.Fatalf("anchors = %+v", idx.Anchors)
	}
	for i, a := range idx.Anchors {
		if a != want[i] {
			t.Fatalf("anchor %d = %+v, want %+v", i, a, want[i])
		}
	}
}
