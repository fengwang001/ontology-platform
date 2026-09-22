package unknown_test

import (
	"bytes"
	"errors"
	"testing"

	"ontology/unknown"
)

func TestAddCopiesPayload(t *testing.T) {
	var f unknown.Fields
	src := []byte{1, 2, 3}
	if err := f.Add(unknown.Field{Number: 7, Type: unknown.Bytes, Payload: src}, -1); err != nil {
		t.Fatal(err)
	}
	src[0] = 99
	if got := f.At(0).Payload[0]; got != 1 {
		t.Fatalf("stored payload aliases input: %d", got)
	}
}

func TestRepeatedKeptInOrder(t *testing.T) {
	var f unknown.Fields
	for i := 0; i < 3; i++ {
		if err := f.Add(unknown.Field{Number: 5, Type: unknown.Varint, Payload: []byte{byte(i + 1)}}, -1); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	buf.Write(f.AppendWrite(nil))
	want := []byte{5, 0, 1, 5, 0, 2, 5, 0, 3}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("got %v want %v", buf.Bytes(), want)
	}
}

func TestLimitRejectedAtomically(t *testing.T) {
	var f unknown.Fields
	if err := f.Add(unknown.Field{Number: 1, Type: unknown.Varint, Payload: []byte{0}}, 1); err != nil {
		t.Fatal(err)
	}
	err := f.Add(unknown.Field{Number: 2, Type: unknown.Varint, Payload: []byte{0}}, 1)
	if !errors.Is(err, unknown.ErrTooManyUnknown) {
		t.Fatalf("got %v", err)
	}
	if f.Len() != 1 {
		t.Fatalf("partial state leaked: len=%d", f.Len())
	}
}

func TestSortStable(t *testing.T) {
	var f unknown.Fields
	for _, n := range []uint64{3, 1, 3, 2, 1} {
		_ = f.Add(unknown.Field{Number: n, Type: unknown.Varint, Payload: []byte{byte(n)}}, -1)
	}
	f.Sort()
	got := []uint64{f.At(0).Number, f.At(1).Number, f.At(2).Number, f.At(3).Number, f.At(4).Number}
	want := []uint64{1, 1, 2, 3, 3}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("order=%v want %v", got, want)
		}
	}
	if f.At(0).Payload[0] != 1 || f.At(1).Payload[0] != 1 {
		t.Fatal("tie order not stable")
	}
}
