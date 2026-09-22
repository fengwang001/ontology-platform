package unknown

import (
	"bytes"
	"testing"
)

func field(num uint64, raw ...byte) Field {
	return Field{Number: num, Type: 0, Raw: raw}
}

func TestSetKeepsOrderAndDuplicates(t *testing.T) {
	var s Set
	s.Add(field(7, 0x07, 0x00, 0x01))
	s.Add(field(3, 0x03, 0x00, 0x02))
	s.Add(field(7, 0x07, 0x00, 0x03))
	if s.Len() != 3 {
		t.Fatalf("Len=%d", s.Len())
	}
	want := []byte{0x07, 0x00, 0x01, 0x03, 0x00, 0x02, 0x07, 0x00, 0x03}
	if got := s.AppendTo(nil); !bytes.Equal(got, want) {
		t.Fatalf("AppendTo = %v, want %v", got, want)
	}
	// Duplicates with the same number keep their relative order.
	if s.At(0).Raw[2] != 0x01 || s.At(2).Raw[2] != 0x03 {
		t.Fatalf("duplicate order changed: %v %v", s.At(0).Raw, s.At(2).Raw)
	}
}

func TestSetCopiesInput(t *testing.T) {
	raw := []byte{0x07, 0x00, 0x01}
	var s Set
	s.Add(field(7, raw...))
	raw[2] = 0xff
	if s.At(0).Raw[2] != 0x01 {
		t.Fatal("Set aliases caller slice")
	}
}

func TestSortedStable(t *testing.T) {
	var s Set
	s.Add(field(7, 0x01))
	s.Add(field(3, 0x02))
	s.Add(field(7, 0x03))
	got := s.Sorted()
	if got[0].Number != 3 || got[1].Number != 7 || got[2].Number != 7 {
		t.Fatalf("Sorted = %+v", got)
	}
	if got[1].Raw[0] != 0x01 || got[2].Raw[0] != 0x03 {
		t.Fatal("sort not stable")
	}
	// Sorting must not disturb the write-back order.
	if s.At(0).Number != 7 {
		t.Fatal("Sorted mutated the set")
	}
}

func TestAppendFieldTo(t *testing.T) {
	var s Set
	s.Add(field(1, 0xaa))
	s.Add(field(2, 0xbb))
	if got := s.AppendFieldTo(nil, 1); !bytes.Equal(got, []byte{0xbb}) {
		t.Fatalf("got %v", got)
	}
}
