package segment

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"ontology/event"
)

func writeSegment(t *testing.T, dir string, firstSeq uint64, payloads [][]byte) string {
	t.Helper()
	w, err := Create(dir, firstSeq)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, p := range payloads {
		if err := w.Append(p); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return filepath.Join(dir, Name(firstSeq))
}

func TestAppendAndReadBack(t *testing.T) {
	dir := t.TempDir()
	payloads := [][]byte{[]byte("a"), {}, bytes.Repeat([]byte("x"), 100)}
	path := writeSegment(t, dir, 42, payloads)

	r, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()
	if r.Hdr.FirstSeq != 42 || r.Hdr.Count != uint64(len(payloads)) {
		t.Fatalf("header = %+v", r.Hdr)
	}
	off := uint64(HeaderSize)
	for i, want := range payloads {
		got, n, err := event.Decode(r.Section(off))
		if err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("event %d payload mismatch", i)
		}
		off += uint64(n)
	}
	if off != uint64(r.Size()) {
		t.Fatalf("consumed %d bytes, file size %d", off, r.Size())
	}
}

func TestSegmentChainContinuity(t *testing.T) {
	dir := t.TempDir()
	sizes := []int{10, 1, 250}
	first := uint64(1000)
	var paths []string
	for _, n := range sizes {
		payloads := make([][]byte, n)
		for i := range payloads {
			payloads[i] = []byte(fmt.Sprintf("p-%d", i))
		}
		paths = append(paths, writeSegment(t, dir, first, payloads))
		first += uint64(n)
	}
	listed, err := List(dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != len(paths) {
		t.Fatalf("listed %d segments, want %d", len(listed), len(paths))
	}
	for i := 0; i+1 < len(listed); i++ {
		a, err := Open(listed[i])
		if err != nil {
			t.Fatalf("open %s: %v", listed[i], err)
		}
		b, err := Open(listed[i+1])
		if err != nil {
			t.Fatalf("open %s: %v", listed[i+1], err)
		}
		if a.Hdr.End() != b.Hdr.FirstSeq {
			t.Fatalf("gap between %s and %s: end=%d next=%d",
				listed[i], listed[i+1], a.Hdr.End(), b.Hdr.FirstSeq)
		}
		a.Close()
		b.Close()
	}
}

func TestOpenErrors(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"short header", []byte("SEG"), ErrShortHeader},
		{"bad magic", append([]byte("XXXX"), make([]byte, HeaderSize-4)...), ErrBadMagic},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(dir, tc.name+".log")
			if err := os.WriteFile(p, tc.data, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(p); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}
