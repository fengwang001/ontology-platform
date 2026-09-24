package sparse

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ontology/event"
	"ontology/segment"
)

func buildSegment(t *testing.T, n, every int) ([]byte, *Index) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "seg")
	w, err := segment.Create(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	x := &Index{Every: every}
	for i := 0; i < n; i++ {
		off, err := w.SeekOffset()
		if err != nil {
			t.Fatal(err)
		}
		pay := []byte(nil)
		if i%2 == 0 {
			pay = []byte{byte(i), byte(i >> 4)}
		}
		if err := w.Append(event.Event{Seq: uint64(i), Payload: pay}); err != nil {
			t.Fatal(err)
		}
		x.Add(i, uint64(i), uint64(off))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return raw, x
}

func TestRebuildByteIdentical(t *testing.T) {
	cases := []struct {
		name  string
		n     int
		every int
	}{
		{"small dense", 50, 8},
		{"medium", 1000, 128},
		{"every one", 17, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, orig := buildSegment(t, tc.n, tc.every)
			reb, err := Rebuild(raw, tc.every)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(reb.Marshal(), orig.Marshal()) {
				t.Fatalf("rebuilt index differs: %d vs %d bytes",
					len(reb.Marshal()), len(orig.Marshal()))
			}
			// Delete the index file, rebuild from segment, compare on disk.
			p := filepath.Join(t.TempDir(), "seg.idx")
			if err := orig.Save(p); err != nil {
				t.Fatal(err)
			}
			ondisk, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			os.Remove(p)
			if err := reb.Save(p); err != nil {
				t.Fatal(err)
			}
			rebuilt, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(ondisk, rebuilt) {
				t.Fatal("index rebuilt after deletion differs byte for byte")
			}
		})
	}
}

func TestLookupPositions(t *testing.T) {
	// Anchors at seq 0,128,256,... ; inspect three from positions.
	_, x := buildSegment(t, 1000, 128)
	cases := []struct {
		name   string
		from   uint64
		wantOK bool
		want   uint64
		skip   uint64
	}{
		{"from equals anchor", 256, true, 256, 0},
		{"from between anchors", 300, true, 256, 44},
		{"from before first anchor", 5, true, 0, 5},
		{"from below min seq", 0, true, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, ok := x.Lookup(tc.from)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v", ok, tc.wantOK)
			}
			if ok && (e.Seq != tc.want || tc.from-e.Seq != tc.skip) {
				t.Fatalf("anchor=%d skip=%d want anchor=%d skip=%d",
					e.Seq, tc.from-e.Seq, tc.want, tc.skip)
			}
		})
	}
	if _, ok := x.Lookup(1_000_000); !ok {
		t.Fatal("from past end should still yield last anchor")
	}
}

func TestIndexErrors(t *testing.T) {
	x := &Index{Every: 4, Entries: []Entry{{Seq: 1, Off: 9}}}
	raw := x.Marshal()
	cases := []struct {
		name string
		buf  []byte
		want error
	}{
		{"too short", raw[:3], ErrShortIndex},
		{"bad magic", flip(raw, 0), ErrBadIndexMagic},
		{"unaligned body", append(raw, 0, 1), ErrShortIndex},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Unmarshal(tc.buf); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
		})
	}
}

func flip(b []byte, i int) []byte {
	out := append([]byte(nil), b...)
	out[i] ^= 0xFF
	return out
}
