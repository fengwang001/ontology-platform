package serve

import (
	"bytes"
	"ontology/source"
	"reflect"
	"testing"
)

func TestZeroValueBeforeAssemble(t *testing.T) {
	a := NewAssembler(Config{}, source.Bytes([]byte("0123456789")))
	if a.Ranges() != nil || a.Total() != 0 || a.Written() != 0 ||
		a.IsMultipart() || a.Boundary() != "" {
		t.Fatal("queries before Assemble must return zero values")
	}
}

// snapshot captures every queryable value at once.
type snapshot struct {
	ranges    interface{}
	total     int64
	written   int64
	multipart bool
	boundary  string
}

func snap(a *Assembler) snapshot {
	return snapshot{a.Ranges(), a.Total(), a.Written(), a.IsMultipart(), a.Boundary()}
}

func TestQueriesAreStableAndPure(t *testing.T) {
	data := source.Bytes([]byte("0123456789ABCDEFGHIJ"))
	a := assemble(t, Config{Rand: fixedRand()}, data, "bytes=0-3, 10-15")
	first := snap(a)
	second := snap(a)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("two consecutive queries differ: %+v vs %+v", first, second)
	}
	// Querying must not advance any state: a third snapshot after more
	// queries is still identical, and Written has not moved.
	_ = a.Ranges()
	_ = a.Total()
	_ = a.IsMultipart()
	if third := snap(a); !reflect.DeepEqual(first, third) {
		t.Fatal("queries mutated assembler state")
	}
	// The returned slice is a copy: mutating it must not leak back.
	rs := a.Ranges()
	rs[0].From = 999
	if a.Ranges()[0].From == 999 {
		t.Fatal("Ranges exposes internal slice")
	}
	// Written only advances via WriteTo.
	var buf bytes.Buffer
	if _, err := a.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	if a.Written() != a.Total() || a.Written() == 0 {
		t.Fatalf("Written=%d Total=%d", a.Written(), a.Total())
	}
}
