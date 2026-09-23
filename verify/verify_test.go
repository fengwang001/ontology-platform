package verify

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ontology/posting"
	"ontology/segment"
)

func writeSeg(t *testing.T, lists map[string]posting.List) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v.seg")
	if err := segment.Write(path, lists); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func goodLists() map[string]posting.List {
	return map[string]posting.List{
		"a": {{Doc: 0, Pos: []uint32{0, 2}}, {Doc: 3, Pos: []uint32{1}}},
		"b": {{Doc: 1, Pos: []uint32{0}}},
	}
}

func TestFile(t *testing.T) {
	good := writeSeg(t, goodLists())
	data, err := os.ReadFile(good)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	trunc := func(n int) string {
		p := filepath.Join(t.TempDir(), "t.seg")
		if err := os.WriteFile(p, data[:n], 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		return p
	}
	badPos := writeSeg(t, map[string]posting.List{
		"a": {{Doc: 0, Pos: []uint32{3, 1}}},
	})
	dictCut := 24 + bytes.Index(data[24:], []byte("b"))
	cases := []struct {
		name string
		path string
		want error
	}{
		{"valid", good, nil},
		{"header truncated", trunc(10), segment.ErrHeaderIncomplete},
		{"dict truncated", trunc(dictCut), segment.ErrDictIncomplete},
		{"postings truncated", trunc(len(data) - 6), segment.ErrPostingsIncomplete},
		{"crc truncated", trunc(len(data) - 2), segment.ErrCRCMismatch},
		{"positions not ascending", badPos, ErrInvariant},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := File(tc.path)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("want nil, got %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}
