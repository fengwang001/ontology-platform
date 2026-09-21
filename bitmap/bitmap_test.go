package bitmap

import (
	"bytes"
	"math"
	"testing"
)

// targetSet is the reference set used by the four-path uniqueness
// test: {1,2,3} ∪ [100,199] ∪ {5000}.
func buildPathPerBit() *Bitmap {
	b := New()
	for _, x := range []uint32{1, 2, 3} {
		b.Set(x)
	}
	for x := uint32(100); x <= 199; x++ {
		b.Set(x)
	}
	b.Set(5000)
	return b
}

func buildPathBatch() *Bitmap {
	b := New()
	b.SetRange(1, 3)
	b.SetRange(100, 199)
	b.Set(5000)
	return b
}

func buildPathUnion() *Bitmap {
	a := New()
	a.SetRange(1, 3)
	a.Set(5000)
	c := New()
	c.SetRange(100, 199)
	u, _ := a.Union(c)
	return u
}

func buildPathSetThenClear() *Bitmap {
	b := New()
	b.SetRange(0, 6000)     // superset
	b.ClearRange(4, 99)     // punch holes
	b.ClearRange(200, 6000) // shrink tail
	b.Clear(0)              // drop leading zero-ish bit
	b.Set(5000)             // re-add cleared bit
	return b
}

func TestCanonicalEncodingFourPaths(t *testing.T) {
	want := buildPathPerBit().Bytes()
	if len(want) == 0 {
		t.Fatal("reference encoding is empty")
	}
	paths := map[string]*Bitmap{
		"per-bit":        buildPathPerBit(),
		"batch":          buildPathBatch(),
		"union":          buildPathUnion(),
		"set-then-clear": buildPathSetThenClear(),
	}
	for name, b := range paths {
		if err := b.Verify(); err != nil {
			t.Fatalf("%s: Verify: %v", name, err)
		}
		if got := b.Bytes(); !bytes.Equal(got, want) {
			t.Fatalf("%s: encoding %v != reference %v", name, got, want)
		}
		if got := b.Count(); got != 3+100+1 {
			t.Fatalf("%s: Count = %d", name, got)
		}
	}
}

func TestEmptySetEncoding(t *testing.T) {
	b := New()
	if got := b.Bytes(); len(got) != 0 {
		t.Fatalf("empty set encoding = %v, want empty", got)
	}
	if got := b.Count(); got != 0 {
		t.Fatalf("empty Count = %d", got)
	}
	if _, ok := b.Min(); ok {
		t.Fatal("empty Min ok = true")
	}
	if _, ok := b.Max(); ok {
		t.Fatal("empty Max ok = true")
	}
}

func TestSetClearIdempotent(t *testing.T) {
	b := New()
	b.SetRange(10, 20)
	before := b.Bytes()
	b.Set(15) // already 1
	b.Set(15)
	if !bytes.Equal(b.Bytes(), before) {
		t.Fatal("redundant Set changed encoding")
	}
	b.Clear(99) // already 0
	b.Clear(0)
	if !bytes.Equal(b.Bytes(), before) {
		t.Fatal("redundant Clear changed encoding")
	}
	// Set then Clear restores the exact original bytes.
	c := New()
	c.SetRange(10, 20)
	c.Set(500)
	c.Clear(500)
	if !bytes.Equal(c.Bytes(), before) {
		t.Fatal("Set+Clear did not restore encoding")
	}
	// Clearing a whole 1-run must merge the neighbouring 0-runs.
	d := New()
	d.SetRange(10, 20)
	d.SetRange(30, 40)
	d.ClearRange(10, 20)
	e := New()
	e.SetRange(30, 40)
	if !bytes.Equal(d.Bytes(), e.Bytes()) {
		t.Fatal("clearing a run did not merge adjacent zero runs")
	}
}

func TestMaxUint32Boundary(t *testing.T) {
	b := New()
	b.Set(math.MaxUint32)
	if !b.Contains(math.MaxUint32) {
		t.Fatal("MaxUint32 not set")
	}
	if got := b.Count(); got != 1 {
		t.Fatalf("Count = %d", got)
	}
	if got, _ := b.Max(); got != math.MaxUint32 {
		t.Fatalf("Max = %d", got)
	}
	if got, _ := b.Min(); got != math.MaxUint32 {
		t.Fatalf("Min = %d", got)
	}
	if err := b.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestFullUniverseSingleRun(t *testing.T) {
	b := New()
	b.SetRange(0, math.MaxUint32)
	if got := b.Count(); got != 1<<32 {
		t.Fatalf("Count = %d, want 2^32", got)
	}
	// One 1-run of length 2^32: 1 value byte + 5 uvarint bytes.
	if got := len(b.Bytes()); got != 6 {
		t.Fatalf("full-universe encoding length = %d, want 6", got)
	}
	if got, _ := b.Max(); got != math.MaxUint32 {
		t.Fatalf("Max = %d", got)
	}
	if got, _ := b.Min(); got != 0 {
		t.Fatalf("Min = %d", got)
	}
	// Clearing the top bit must not overflow and must shrink Count.
	b.Clear(math.MaxUint32)
	if got := b.Count(); got != 1<<32-1 {
		t.Fatalf("Count after Clear = %d", got)
	}
	if got, _ := b.Max(); got != math.MaxUint32-1 {
		t.Fatalf("Max after Clear = %d", got)
	}
	if err := b.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyDetectsCorruption(t *testing.T) {
	cases := []struct {
		name string
		runs []run
	}{
		{"zero length", []run{{val: 1, length: 0}}},
		{"unmerged neighbours", []run{{val: 1, length: 3}, {val: 1, length: 2}}},
		{"trailing zero run", []run{{val: 1, length: 3}, {val: 0, length: 2}}},
	}
	for _, tc := range cases {
		b := &Bitmap{runs: tc.runs}
		if err := b.Verify(); err == nil {
			t.Fatalf("%s: Verify passed on corrupt runs", tc.name)
		}
	}
	good := &Bitmap{runs: []run{{val: 0, length: 3}, {val: 1, length: 2}}}
	if err := good.Verify(); err != nil {
		t.Fatalf("canonical runs rejected: %v", err)
	}
}
