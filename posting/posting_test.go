package posting

import (
	"bytes"
	"reflect"
	"testing"
)

func TestBuildAndDedup(t *testing.T) {
	b := NewBuilder()
	b.Add("A", 1, 5)
	b.Add("A", 1, 5) // 同文档同位置重复
	b.Add("A", 1, 2)
	b.Add("A", 0, 0)
	b.Add("", 0, 0) // 空词元合法
	b.Add("B", 0, 3)
	idx := b.Build()
	if idx.DupCount != 1 {
		t.Fatalf("DupCount = %d, want 1", idx.DupCount)
	}
	wantA := &Chain{Term: "A", Docs: []Doc{
		{ID: 0, Positions: []uint32{0}},
		{ID: 1, Positions: []uint32{2, 5}},
	}}
	if got := idx.Chain("A"); !reflect.DeepEqual(got, wantA) {
		t.Fatalf("chain A = %#v, want %#v", got, wantA)
	}
	if got := idx.Chain(""); got == nil || len(got.Docs) != 1 {
		t.Fatalf("empty term chain missing: %#v", got)
	}
	if got := idx.Terms(); !reflect.DeepEqual(got, []string{"", "A", "B"}) {
		t.Fatalf("terms = %v", got)
	}
	if idx.Chain("ZZ") != nil {
		t.Fatalf("missing term should be nil")
	}
}

func TestEncodeDecode(t *testing.T) {
	cases := []struct {
		name string
		doc  *Chain
	}{
		{"empty", &Chain{Term: "x", Docs: []Doc{}}},
		{"single", &Chain{Term: "x", Docs: []Doc{{ID: 7, Positions: []uint32{0}}}}},
		{"multi", &Chain{Term: "x", Docs: []Doc{
			{ID: 3, Positions: []uint32{1, 4}},
			{ID: 10, Positions: []uint32{0, 2, 9}},
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := EncodeChain(&buf, tc.doc); err != nil {
				t.Fatal(err)
			}
			got, _, err := DecodeChain(bytes.NewReader(buf.Bytes()), "x")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.doc) {
				t.Fatalf("roundtrip = %#v, want %#v", got, tc.doc)
			}
		})
	}
}

func TestDocGE(t *testing.T) {
	c := &Chain{Docs: []Doc{{ID: 1}, {ID: 4}, {ID: 9}}}
	cases := []struct{ q, want uint32 }{
		{0, 0}, {1, 0}, {2, 1}, {4, 1}, {8, 2}, {9, 2}, {100, 3},
	}
	for _, tc := range cases {
		if got := c.DocGE(tc.q); uint32(got) != tc.want {
			t.Errorf("DocGE(%d) = %d, want %d", tc.q, got, tc.want)
		}
	}
}
