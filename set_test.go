package ontology

import (
	"bytes"
	"math"
	"testing"
)

// targetSet returns the reference set used by the canonical-encoding
// tests: {1,2,3, 7, 100..199, 500}.
func targetBits() []uint32 {
	bits := []uint32{1, 2, 3, 7, 500}
	for b := uint32(100); b < 200; b++ {
		bits = append(bits, b)
	}
	return bits
}

// TestCanonicalEncoding builds the same set through four different
// construction paths and requires byte-identical encodings.
func TestCanonicalEncoding(t *testing.T) {
	bits := targetBits()

	// Path 1: set bits one by one, in order.
	p1 := New()
	for _, b := range bits {
		p1.Set(b)
	}

	// Path 2: same bits, reverse order, plus redundant duplicate sets.
	p2 := New()
	for i := len(bits) - 1; i >= 0; i-- {
		p2.Set(bits[i])
		p2.Set(bits[i]) // idempotent duplicate
	}

	// Path 3: union of two overlapping sets.
	a, b := New(), New()
	for _, x := range bits[:len(bits)/2] {
		a.Set(x)
	}
	for _, x := range bits[len(bits)/4:] {
		b.Set(x)
	}
	p3, _ := a.Union(b)

	// Path 4: set too many bits, then clear the extras.
	p4 := New()
	for x := uint32(0); x <= 600; x++ {
		p4.Set(x)
	}
	for x := uint32(0); x <= 600; x++ {
		found := false
		for _, keep := range bits {
			if x == keep {
				found = true
				break
			}
		}
		if !found {
			p4.Clear(x)
		}
	}

	want := p1.Bytes()
	for i, p := range []*Set{p2, p3, p4} {
		if !bytes.Equal(want, p.Bytes()) {
			t.Fatalf("path %d encoding differs:\n got %x\nwant %x", i+2, p.Bytes(), want)
		}
		if err := p.Verify(); err != nil {
			t.Fatalf("path %d Verify: %v", i+2, err)
		}
	}
}

// TestIdempotentNoDegeneration: setting an already-set bit and clearing
// an already-clear bit must not change the encoding; set-then-clear must
// restore the exact original bytes.
func TestIdempotentNoDegeneration(t *testing.T) {
	s := New()
	for _, b := range targetBits() {
		s.Set(b)
	}
	orig := s.Bytes()

	s.Set(7)     // already set
	s.Clear(42)  // already clear
	s.Clear(601) // already clear, beyond last run
	if !bytes.Equal(orig, s.Bytes()) {
		t.Fatal("idempotent ops changed encoding")
	}

	s.Set(42)  // bridge nothing: new singleton run
	s.Set(300) // another singleton
	s.Clear(42)
	s.Clear(300)
	if !bytes.Equal(orig, s.Bytes()) {
		t.Fatal("set-then-clear did not restore original encoding")
	}

	// Fill a gap completely, then clear it again.
	s.Set(4)
	s.Set(5)
	s.Set(6) // now 1..7 is one merged run
	s.Clear(4)
	s.Clear(5)
	s.Clear(6)
	if !bytes.Equal(orig, s.Bytes()) {
		t.Fatal("fill-gap-then-clear did not restore original encoding")
	}
	if err := s.Verify(); err != nil {
		t.Fatal(err)
	}
}

// TestEmptySetEncoding: the empty set has the deterministic, shortest
// encoding: zero bytes.
func TestEmptySetEncoding(t *testing.T) {
	s := New()
	if got := s.Bytes(); got == nil || len(got) != 0 {
		t.Fatalf("empty encoding = %v, want non-nil empty slice", got)
	}
	if s.Count() != 0 {
		t.Fatal("empty set count != 0")
	}
	if _, ok := s.Min(); ok {
		t.Fatal("empty set has a Min")
	}
	if _, ok := s.Max(); ok {
		t.Fatal("empty set has a Max")
	}
	if err := s.Verify(); err != nil {
		t.Fatal(err)
	}
}

// TestMaxUint32: the top of the domain can be set without overflow, and
// a single run may span the whole domain with length 2^32.
func TestMaxUint32(t *testing.T) {
	s := New()
	s.Set(math.MaxUint32)
	if !s.Contains(math.MaxUint32) {
		t.Fatal("MaxUint32 not set")
	}
	if s.Count() != 1 {
		t.Fatalf("count = %d, want 1", s.Count())
	}
	if hi, _ := s.Max(); hi != math.MaxUint32 {
		t.Fatalf("max = %d, want MaxUint32", hi)
	}

	// A single run may span the whole domain: length 2^32. Build it
	// directly through the run list to avoid iterating 2^32 bits.
	fullRun := &Set{runs: []interval{{0, math.MaxUint32}}}
	if got := fullRun.Count(); got != 1<<32 {
		t.Fatalf("full-domain count = %d, want 2^32", got)
	}
	if err := fullRun.Verify(); err != nil {
		t.Fatal(err)
	}
	if lo, _ := fullRun.Min(); lo != 0 {
		t.Fatal("full-domain min != 0")
	}
	if hi, _ := fullRun.Max(); hi != math.MaxUint32 {
		t.Fatal("full-domain max != MaxUint32")
	}
	// Encoding must be a single run: start=0, length=2^32 (5-byte varint).
	if got := fullRun.Bytes(); len(got) != 6 {
		t.Fatalf("full-domain encoding = %x (len %d), want 6 bytes", got, len(got))
	}

	// Clearing MaxUint32 from a run ending at MaxUint32 must not overflow.
	c := New()
	c.Set(math.MaxUint32 - 1)
	c.Set(math.MaxUint32)
	c.Clear(math.MaxUint32)
	want := New()
	want.Set(math.MaxUint32 - 1)
	if !bytes.Equal(want.Bytes(), c.Bytes()) {
		t.Fatal("clearing MaxUint32 corrupted encoding")
	}
}
