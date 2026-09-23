package posting

import (
	"errors"
	"reflect"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		list List
	}{
		{"empty", nil},
		{"single doc single pos", List{{Doc: 0, Pos: []uint32{0}}}},
		{"multi doc multi pos", List{
			{Doc: 0, Pos: []uint32{0, 3, 7}},
			{Doc: 2, Pos: []uint32{1}},
			{Doc: 100, Pos: []uint32{0, 100000}},
		}},
		{"doc zero and large gap", List{
			{Doc: 0, Pos: []uint32{5}},
			{Doc: 1 << 20, Pos: []uint32{0}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decode(Encode(tc.list))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(tc.list) == 0 && len(got) != 0 {
				t.Fatalf("want empty, got %v", got)
			}
			if !reflect.DeepEqual([]Entry(tc.list), []Entry(got)) {
				t.Fatalf("round trip mismatch: want %v got %v", tc.list, got)
			}
		})
	}
}

func TestDecodeCorrupt(t *testing.T) {
	good := Encode(List{{Doc: 1, Pos: []uint32{0, 2}}})
	cases := []struct {
		name string
		data []byte
	}{
		{"truncated varint", good[:len(good)-1]},
		{"zero doc delta", []byte{0}},
		{"zero pos count", []byte{1, 0}},
		{"zero pos delta", []byte{1, 1, 0}},
		{"empty varint", []byte{0x80}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Decode(tc.data); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("want ErrCorrupt, got %v", err)
			}
		})
	}
}

func TestBuilderDuplicates(t *testing.T) {
	var b Builder
	adds := []struct {
		doc, pos uint32
		ok       bool
	}{
		{0, 0, true},
		{0, 1, true},
		{0, 1, false}, // exact duplicate
		{0, 0, false}, // duplicate, out of order position
		{1, 0, true},
		{1, 5, true},
		{1, 5, false},
	}
	for i, a := range adds {
		if got := b.Add(a.doc, a.pos); got != a.ok {
			t.Fatalf("add %d: want ok=%v got %v", i, a.ok, got)
		}
	}
	if b.Dups() != 3 {
		t.Fatalf("want 3 dups, got %d", b.Dups())
	}
	want := List{{Doc: 0, Pos: []uint32{0, 1}}, {Doc: 1, Pos: []uint32{0, 5}}}
	if !reflect.DeepEqual(b.List(), want) {
		t.Fatalf("want %v got %v", want, b.List())
	}
}
