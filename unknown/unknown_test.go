package unknown_test

import (
	"bytes"
	"testing"

	"ontology/unknown"
)

func TestAddPreservesOrderAndDuplicates(t *testing.T) {
	var f unknown.Fields
	f.Add(7, 1, []byte{0x07, 0x01, 0x01, 'a'})
	f.Add(3, 0, []byte{0x03, 0x00, 0x2A})
	f.Add(7, 0, []byte{0x07, 0x00, 0x01}) // duplicate number, kept
	if f.Len() != 3 {
		t.Fatalf("Len = %d, want 3", f.Len())
	}
	for i, num := range []uint64{7, 3, 7} {
		if f.At(i).Num != num {
			t.Fatalf("At(%d).Num = %d, want %d", i, f.At(i).Num, num)
		}
	}
}

func TestAddCopiesRaw(t *testing.T) {
	var f unknown.Fields
	raw := []byte{0x07, 0x01, 0x01, 'a'}
	f.Add(7, 1, raw)
	raw[3] = 'X' // mutate caller buffer afterwards
	if f.At(0).Raw[3] != 'a' {
		t.Fatal("Add must copy the raw bytes")
	}
}

func TestSortIsStable(t *testing.T) {
	var f unknown.Fields
	f.Add(7, 1, []byte{'a'})
	f.Add(3, 0, []byte{'b'})
	f.Add(7, 0, []byte{'c'})
	f.Add(3, 0, []byte{'d'})
	f.Sort()
	want := []struct {
		num uint64
		raw byte
	}{{3, 'b'}, {3, 'd'}, {7, 'a'}, {7, 'c'}}
	for i, w := range want {
		if f.At(i).Num != w.num || f.At(i).Raw[0] != w.raw {
			t.Fatalf("At(%d) = {%d,%c}, want {%d,%c}", i,
				f.At(i).Num, f.At(i).Raw[0], w.num, w.raw)
		}
	}
}

func TestAppendTo(t *testing.T) {
	var f unknown.Fields
	f.Add(7, 1, []byte{0x01, 0x02})
	f.Add(9, 0, []byte{0x03})
	got := f.AppendTo([]byte{0x00})
	if !bytes.Equal(got, []byte{0x00, 0x01, 0x02, 0x03}) {
		t.Fatalf("AppendTo = %v", got)
	}
	// AppendTo must not mutate the container.
	if !bytes.Equal(f.AppendTo(nil), []byte{0x01, 0x02, 0x03}) {
		t.Fatal("AppendTo mutated the container")
	}
}
