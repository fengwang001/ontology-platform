package ontology

import (
	"bytes"
	"math"
	"testing"
)

// target members used by the four construction paths.
var targetMembers = []uint32{1, 2, 3, 100, 101, 102, 103, 5000, 70000}

func buildOneByOne() *Set {
	s := New()
	for _, p := range targetMembers {
		s.Set(p)
	}
	return s
}

func buildReverseAndRedundant() *Set {
	s := New()
	for i := len(targetMembers) - 1; i >= 0; i-- {
		s.Set(targetMembers[i])
		s.Set(targetMembers[i]) // idempotent duplicate
	}
	return s
}

func buildViaUnion() *Set {
	a, b := New(), New()
	for i, p := range targetMembers {
		if i%2 == 0 {
			a.Set(p)
		} else {
			b.Set(p)
		}
	}
	return a.Union(b)
}

func buildSupersetThenClear() *Set {
	s := New()
	for p := uint32(0); p <= 70000; p++ {
		s.Set(p)
	}
	want := map[uint32]bool{}
	for _, p := range targetMembers {
		want[p] = true
	}
	for p := uint32(0); p <= 70000; p++ {
		if !want[p] {
			s.Clear(p)
		}
	}
	return s
}

func TestEncodingUniqueAcrossConstructionPaths(t *testing.T) {
	ref := buildOneByOne().Bytes()
	paths := map[string]*Set{
		"reverse+dup":   buildReverseAndRedundant(),
		"union":         buildViaUnion(),
		"supersetClear": buildSupersetThenClear(),
	}
	for name, s := range paths {
		if !bytes.Equal(ref, s.Bytes()) {
			t.Fatalf("path %s: encoding differs\n got %x\nwant %x", name, s.Bytes(), ref)
		}
		if !s.Verify() {
			t.Fatalf("path %s: Verify failed", name)
		}
	}
}

func TestAdjacentRunsMerged(t *testing.T) {
	s := New()
	s.Set(5)
	s.Set(6)
	s.Set(7)
	if s.Runs() != 2 { // leading zero run + one merged 1-run
		t.Fatalf("runs = %d, want 2", s.Runs())
	}
	s.Set(4) // extends the 1-run on the left
	if s.Runs() != 2 || !s.Verify() {
		t.Fatalf("runs = %d after merge-left", s.Runs())
	}
}

func TestNoZeroLengthRuns(t *testing.T) {
	s := New()
	for p := uint32(0); p < 64; p++ {
		s.Set(p)
	}
	for p := uint32(0); p < 64; p += 2 {
		s.Clear(p) // splits runs; zero-length fragments must vanish
	}
	if !s.Verify() {
		t.Fatal("Verify failed after splitting clears")
	}
}

func TestNoTrailingZeroRun(t *testing.T) {
	s := New()
	s.Set(10)
	s.Set(20)
	s.Clear(20) // highest bit: must not leave a trailing zero run
	if !s.Verify() {
		t.Fatal("trailing zero run retained")
	}
	if got := s.Bytes(); !bytes.Equal(got, mustSingleBit(10)) {
		t.Fatalf("encoding = %x, want %x", got, mustSingleBit(10))
	}
}

func mustSingleBit(p uint32) []byte {
	s := New()
	s.Set(p)
	return s.Bytes()
}

func TestEmptyEncodingIsShortest(t *testing.T) {
	if got := New().Bytes(); !bytes.Equal(got, []byte{0}) {
		t.Fatalf("empty encoding = %x, want 00", got)
	}
}

func TestSetThenClearRestoresEncoding(t *testing.T) {
	s := New()
	for _, p := range []uint32{3, 9, 27} {
		s.Set(p)
	}
	before := s.Bytes()
	s.Set(13)
	s.Clear(13)
	if !bytes.Equal(before, s.Bytes()) {
		t.Fatalf("encoding not restored: %x vs %x", before, s.Bytes())
	}
	s.Clear(13) // clearing a 0 bit is idempotent
	if !bytes.Equal(before, s.Bytes()) {
		t.Fatal("clearing a zero bit changed the encoding")
	}
}

func TestMaxUint32NoOverflow(t *testing.T) {
	s := New()
	s.Set(math.MaxUint32)
	if !s.Contains(math.MaxUint32) || s.Count() != 1 {
		t.Fatal("MaxUint32 not set")
	}
	hi, ok := s.Max()
	if !ok || hi != math.MaxUint32 {
		t.Fatalf("Max = %d, %v", hi, ok)
	}
	if !s.Verify() {
		t.Fatal("Verify failed")
	}
}

func TestFullUniverseRunEncoding(t *testing.T) {
	// A single 1-run of length 2^32 covers the whole universe.
	s := &Set{runs: []run{{1, 1 << 32}}}
	if !s.Verify() {
		t.Fatal("Verify failed for full-universe run")
	}
	if s.Count() != 1<<32 {
		t.Fatalf("Count = %d, want 2^32", s.Count())
	}
	if lo, _ := s.Min(); lo != 0 {
		t.Fatalf("Min = %d", lo)
	}
	if hi, _ := s.Max(); hi != math.MaxUint32 {
		t.Fatalf("Max = %d", hi)
	}
	// uvarint(count=1) + uvarint(2^33|1) = 1 + 5 bytes.
	if got := s.Bytes(); len(got) != 6 {
		t.Fatalf("full-universe encoding = %x (%d bytes)", got, len(got))
	}
}
