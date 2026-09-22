package unknown

import (
	"bytes"
	"testing"
)

func TestSetPreservesArrivalOrder(t *testing.T) {
	var s Set
	in := []Field{
		{Number: 9, Raw: []byte{0x09, 0x00, 0x01}},
		{Number: 4, Raw: []byte{0x04, 0x01, 0x01, 0xAA}},
		{Number: 9, Raw: []byte{0x09, 0x00, 0x02}},
	}
	for _, f := range in {
		s.Add(f)
	}
	if s.Len() != 3 {
		t.Fatalf("Len = %d, want 3", s.Len())
	}
	var want []byte
	for _, f := range in {
		want = append(want, f.Raw...)
	}
	if got := s.AppendTo(nil); !bytes.Equal(got, want) {
		t.Fatalf("AppendTo = %x, want %x", got, want)
	}
	for i, f := range in {
		if s.At(i).Number != f.Number {
			t.Fatalf("At(%d).Number = %d, want %d", i, s.At(i).Number, f.Number)
		}
	}
}

func TestSetRepeatedNumbersNotMerged(t *testing.T) {
	var s Set
	s.Add(Field{Number: 7, Raw: []byte{1}})
	s.Add(Field{Number: 7, Raw: []byte{2}})
	s.Add(Field{Number: 7, Raw: []byte{3}})
	if s.Len() != 3 {
		t.Fatalf("repeated fields were merged: Len = %d", s.Len())
	}
	got := s.AppendTo(nil)
	if !bytes.Equal(got, []byte{1, 2, 3}) {
		t.Fatalf("order changed: %x", got)
	}
}

func TestFieldsReturnsCopy(t *testing.T) {
	var s Set
	s.Add(Field{Number: 1, Raw: []byte{0xAA}})
	cp := s.Fields()
	cp[0].Number = 99
	if s.At(0).Number != 1 {
		t.Fatal("Fields exposed internal state")
	}
}

func TestZeroSetUsable(t *testing.T) {
	var s Set
	if s.Len() != 0 {
		t.Fatal("zero Set not empty")
	}
	if got := s.AppendTo(nil); got != nil {
		t.Fatalf("zero Set AppendTo = %x, want nil", got)
	}
}
