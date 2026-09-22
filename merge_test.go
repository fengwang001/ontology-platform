package hll

import (
	"errors"
	"testing"
)

// TestMergeRegisterWiseMax checks the merged registers equal the
// element-wise maximum of the two sources.
func TestMergeRegisterWiseMax(t *testing.T) {
	a, _ := New(6)
	b, _ := New(6)

	// Interleave register targets with different ranks:
	// a gives register 3 rho=1, b gives register 3 rho=5.
	a.Add(uint64(1)<<63 | 0x03)
	b.Add(uint64(1)<<59 | 0x03)
	// b alone touches register 7.
	b.Add(uint64(1)<<63 | 0x07)
	// a alone touches register 10 with a big rank.
	a.Add(0x0A) // high bits zero => rho upper bound

	m, err := Merge(a, b)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	got := m.InspectRegisters()
	if got[3] != 5 || got[7] != 1 || got[10] != uint8(64-6+1) {
		t.Fatalf("merged registers unexpected: [3]=%d [7]=%d [10]=%d",
			got[3], got[7], got[10])
	}
}

// TestMergeEmptyIsIdentity merges each side with an empty estimator and
// requires a byte-identical register snapshot.
func TestMergeEmptyIsIdentity(t *testing.T) {
	hs := distinctHashes(300)
	src, _ := New(10)
	for _, h := range hs {
		src.Add(h)
	}
	before := src.InspectRegisters()

	empty, _ := New(10)
	m1, err := Merge(src, empty)
	if err != nil {
		t.Fatalf("Merge(src,empty): %v", err)
	}
	m2, err := Merge(empty, src)
	if err != nil {
		t.Fatalf("Merge(empty,src): %v", err)
	}
	if !regsEqual(m1.InspectRegisters(), before) {
		t.Fatal("Merge(src, empty) registers differ from src")
	}
	if !regsEqual(m2.InspectRegisters(), before) {
		t.Fatal("Merge(empty, src) registers differ from src")
	}

	// Two empties also merge to all zeros.
	e2, _ := New(10)
	m3, _ := Merge(empty, e2)
	if !regsEqual(m3.InspectRegisters(), zeroRegs(1024)) {
		t.Fatal("merge of two empties not empty")
	}
}

// TestMergeDoesNotMutateSources snapshots both sources before and after
// Merge and demands byte-identical registers plus unchanged estimates.
func TestMergeDoesNotMutateSources(t *testing.T) {
	ha, hb := distinctHashes(150), distinctHashes(400)
	// Give b a disjoint-looking set via offset (still deterministic).
	for i := range hb {
		hb[i] = splitmix64(uint64(1_000_000 + i))
	}
	a, _ := New(11)
	b, _ := New(11)
	for _, h := range ha {
		a.Add(h)
	}
	for _, h := range hb {
		b.Add(h)
	}

	sa, sb := a.InspectRegisters(), b.InspectRegisters()
	ea, eb := a.Estimate(), b.Estimate()

	if _, err := Merge(a, b); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	if !regsEqual(a.InspectRegisters(), sa) {
		t.Fatal("Merge mutated source a registers")
	}
	if !regsEqual(b.InspectRegisters(), sb) {
		t.Fatal("Merge mutated source b registers")
	}
	if a.Estimate() != ea || b.Estimate() != eb {
		t.Fatalf("Merge mutated source estimates: %d,%d vs %d,%d",
			a.Estimate(), b.Estimate(), ea, eb)
	}
}

// TestMergePrecisionMismatch checks the error is decidable and reports both
// p values.
func TestMergePrecisionMismatch(t *testing.T) {
	a, _ := New(4)
	b, _ := New(16)
	_, err := Merge(a, b)
	if err == nil {
		t.Fatal("Merge of p=4 and p=16 succeeded, want error")
	}
	if !errors.Is(err, ErrPrecisionMismatch) {
		t.Fatalf("error %v does not wrap ErrPrecisionMismatch", err)
	}
	pe, ok := err.(PrecisionMismatchError)
	if !ok || pe.P1 != 4 || pe.P2 != 16 {
		t.Fatalf("error %v does not carry p1=4,p2=16", err)
	}
}

// TestMergeUnionEstimate verifies merged disjoint sets estimate roughly the
// union cardinality (here exact, since both stay sparse).
func TestMergeUnionEstimate(t *testing.T) {
	hs := distinctHashes(50)
	a, _ := New(10)
	b, _ := New(10)
	for i, h := range hs {
		if i%2 == 0 {
			a.Add(h)
		} else {
			b.Add(h)
		}
	}
	m, err := Merge(a, b)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if got := m.Estimate(); got != 50 {
		t.Fatalf("merged disjoint halves estimate = %d, want 50", got)
	}
}

// TestIdempotentRepeatAdds adds one hash one million times and asserts the
// registers, snapshot, and estimate never change after the first add.
func TestIdempotentRepeatAdds(t *testing.T) {
	e, _ := New(8)
	h := uint64(0xDEADBEEFCAFEBABE)

	if got := e.Estimate(); got != 0 {
		t.Fatalf("empty estimate = %d, want 0", got)
	}
	e.Add(h)
	regsOnce := e.InspectRegisters()
	estOnce := e.Estimate()
	if estOnce != 1 {
		t.Fatalf("after one add estimate = %d, want 1", estOnce)
	}
	for i := 0; i < 1_000_000; i++ {
		e.Add(h)
	}
	if !regsEqual(e.InspectRegisters(), regsOnce) {
		t.Fatal("repeated add changed registers")
	}
	if got := e.Estimate(); got != estOnce {
		t.Fatalf("after 1e6 repeats estimate = %d, want %d", got, estOnce)
	}
}
