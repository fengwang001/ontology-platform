package posting

import (
	"reflect"
	"testing"
)

func TestEncodeDecode(t *testing.T) {
	cases := []struct {
		name string
		docs []Doc
	}{
		{"empty", nil},
		{"single", []Doc{{ID: 7, Positions: []uint32{0}}}},
		{"multi", []Doc{
			{ID: 1, Positions: []uint32{0, 3, 10}},
			{ID: 4, Positions: []uint32{2}},
			{ID: 99, Positions: []uint32{1, 5, 6, 1000000}},
		}},
		{"zero-doc", []Doc{{ID: 0, Positions: []uint32{0}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orig := &Chain{Term: "x", Docs: tc.docs}
			got, err := Decode(Encode(orig))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !reflect.DeepEqual(got.Docs, orig.Docs) {
				t.Fatalf("round trip = %+v, want %+v", got.Docs, orig.Docs)
			}
		})
	}
}

func TestDecodeCorrupt(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"truncated-varint", []byte{0x80}},
		{"trailing-bytes", []byte{0, 1}},
		{"missing-doc-delta", []byte{1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Decode(tc.data); err == nil {
				t.Fatal("expected decode error")
			}
		})
	}
}

func TestBuilderAndMerge(t *testing.T) {
	b := NewBuilder()
	events := []struct {
		term     string
		doc, pos uint32
		accept   bool
	}{
		{"", 0, 0, true},
		{"a", 0, 2, true},
		{"a", 0, 0, true},
		{"a", 0, 2, false},
		{"a", 1, 1, true},
		{"b", 1, 0, true},
	}
	for _, e := range events {
		if got := b.Add(e.term, e.doc, e.pos); got != e.accept {
			t.Fatalf("Add(%q,%d,%d)=%v want %v", e.term, e.doc, e.pos, got, e.accept)
		}
	}
	if b.DupAdd != 1 {
		t.Fatalf("DupAdd=%d want 1", b.DupAdd)
	}
	chains := b.Build()
	if chains[0].Term != "" || chains[1].Term != "a" || chains[2].Term != "b" {
		t.Fatalf("terms = %q,%q,%q", chains[0].Term, chains[1].Term, chains[2].Term)
	}
	a := chains[1]
	if a.Docs[0].ID != 0 || !reflect.DeepEqual(a.Docs[0].Positions, []uint32{0, 2}) {
		t.Fatalf("a doc0 = %+v", a.Docs[0])
	}
	if a.Docs[1].ID != 1 || !reflect.DeepEqual(a.Docs[1].Positions, []uint32{1}) {
		t.Fatalf("a doc1 = %+v", a.Docs[1])
	}
	other := &Chain{Term: "a", Docs: []Doc{
		{ID: 0, Positions: []uint32{2, 5}},
		{ID: 2, Positions: []uint32{0}},
	}}
	merged := MergeChains("a", []*Chain{a, other})
	wantMerged := []Doc{
		{ID: 0, Positions: []uint32{0, 2, 5}},
		{ID: 1, Positions: []uint32{1}},
		{ID: 2, Positions: []uint32{0}},
	}
	if !reflect.DeepEqual(merged.Docs, wantMerged) {
		t.Fatalf("merged = %+v", merged.Docs)
	}
}
