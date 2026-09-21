package ontology

import (
	"bytes"
	"math"
	"math/rand"
	"testing"
)

// referenceMembers is the canonical member list shared by the
// four-construction-path tests.
func referenceMembers() []uint32 {
	m := []uint32{1, 2, 3, 100}
	for v := uint32(10); v <= 19; v++ {
		m = append(m, v)
	}
	for v := uint32(1000); v <= 2000; v++ {
		m = append(m, v)
	}
	return m
}

// buildIndividually sets members one by one in ascending order.
func buildIndividually() *Bitmap {
	b := New()
	for _, v := range referenceMembers() {
		b.Set(v)
	}
	return b
}

// buildShuffled sets members one by one in a random order.
func buildShuffled(seed int64) *Bitmap {
	m := referenceMembers()
	rand.New(rand.NewSource(seed)).Shuffle(len(m), func(i, j int) {
		m[i], m[j] = m[j], m[i]
	})
	b := New()
	for _, v := range m {
		b.Set(v)
	}
	return b
}

// buildBulk mixes SetRange and Set calls.
func buildBulk() *Bitmap {
	b := New()
	b.SetRange(1000, 2000)
	b.SetRange(10, 19)
	b.Set(100)
	b.Set(3)
	b.Set(1)
	b.Set(2)
	return b
}

// buildViaUnion constructs the set as the union of two disjoint halves.
func buildViaUnion() *Bitmap {
	a := New()
	a.SetRange(10, 19)
	a.Set(1)
	a.Set(2)
	b := New()
	b.SetRange(1000, 2000)
	b.Set(3)
	b.Set(100)
	u, _ := a.Union(b)
	return u
}

// buildViaSetThenClear sets a superset and clears the extras.
func buildViaSetThenClear() *Bitmap {
	b := buildBulk()
	extra := []uint32{0, 4, 5, 500, 501, 999, 2001, 3000, 4000}
	for _, v := range extra {
		b.Set(v)
	}
	for _, v := range extra {
		b.Clear(v)
	}
	return b
}

func TestUniqueEncodingAcrossConstructionPaths(t *testing.T) {
	want := buildIndividually().Bytes()
	paths := map[string]*Bitmap{
		"shuffled":       buildShuffled(42),
		"bulk":           buildBulk(),
		"union":          buildViaUnion(),
		"set-then-clear": buildViaSetThenClear(),
	}
	for name, b := range paths {
		if got := b.Bytes(); !bytes.Equal(got, want) {
			t.Errorf("path %s: encoding differs\n got %v\nwant %v", name, got, want)
		}
		if err := b.Verify(); err != nil {
			t.Errorf("path %s: Verify failed: %v", name, err)
		}
	}
}

func TestAdjacentSameValueRunsMerged(t *testing.T) {
	b := New()
	for v := uint32(0); v <= 3; v++ {
		b.Set(v)
	}
	if b.Runs() != 1 {
		t.Fatalf("expected 1 merged run, got %d", b.Runs())
	}
	if b.Count() != 4 {
		t.Fatalf("expected count 4, got %d", b.Count())
	}
	// A set not touching bit 0 keeps exactly one leading zero run.
	c := New()
	c.SetRange(5, 8)
	if c.Runs() != 2 || c.Count() != 4 {
		t.Fatalf("runs=%d count=%d, want 2/4", c.Runs(), c.Count())
	}
}

func TestNoZeroLengthOrTrailingZeroRuns(t *testing.T) {
	b := New()
	b.Set(3)
	b.Set(9)
	b.Clear(9) // leaves a would-be trailing zero region
	if err := b.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if b.Runs() != 2 || b.Count() != 1 {
		t.Fatalf("runs=%d count=%d, want 2/1", b.Runs(), b.Count())
	}
	b.Clear(3)
	if b.Runs() != 0 || b.Count() != 0 {
		t.Fatalf("expected empty set, runs=%d count=%d", b.Runs(), b.Count())
	}
}

func TestSetThenClearRestoresEncoding(t *testing.T) {
	b := buildBulk()
	before := b.Bytes()
	b.Set(7)
	b.Clear(7)
	if !bytes.Equal(b.Bytes(), before) {
		t.Fatal("set-then-clear did not restore the original encoding")
	}
	// Idempotence on already-set / already-clear bits.
	b.Set(10)
	b.Clear(7)
	if !bytes.Equal(b.Bytes(), before) {
		t.Fatal("idempotent set/clear changed the encoding")
	}
}

func TestEmptySetEncoding(t *testing.T) {
	got := New().Bytes()
	if !bytes.Equal(got, []byte{0x00}) {
		t.Fatalf("empty set encoding = %v, want [0x00]", got)
	}
}

func TestMaxUint32AndFullUniverse(t *testing.T) {
	b := New()
	b.Set(math.MaxUint32)
	if !b.Contains(math.MaxUint32) || b.Count() != 1 {
		t.Fatal("MaxUint32 not settable")
	}
	if v, _ := b.Max(); v != math.MaxUint32 {
		t.Fatalf("Max = %d, want %d", v, uint32(math.MaxUint32))
	}
	full := New()
	full.SetRange(0, math.MaxUint32)
	if got := full.Count(); got != 1<<32 {
		t.Fatalf("full universe count = %d, want 2^32", got)
	}
	if full.Runs() != 1 {
		t.Fatalf("full universe runs = %d, want 1", full.Runs())
	}
	if v, _ := full.Max(); v != math.MaxUint32 {
		t.Fatalf("full universe Max = %d, want MaxUint32", v)
	}
	if err := full.Verify(); err != nil {
		t.Fatalf("full universe Verify: %v", err)
	}
}
