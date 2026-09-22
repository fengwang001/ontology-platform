package hll

import (
	"errors"
	"testing"
)

// emptyRegs builds a zero register slice of the expected length for
// comparison.
func zeroRegs(m int) []uint8 { return make([]uint8, m) }

func regsEqual(a, b []uint8) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestHandCraftedRegisterUpdates walks hand-built bit patterns through an
// estimator with p=4 (m=16) and asserts every register's exact rank after
// every Add. The cases deliberately cover:
//
//   - a first write to an empty register
//   - a later hit on the same register with a SMALLER rho: value must stay
//   - a later hit with a LARGER rho: value must be replaced exactly
//   - the rho upper bound: high 60 bits all zero (hash low bits nonzero)
func TestHandCraftedRegisterUpdates(t *testing.T) {
	e, err := New(4)
	if err != nil {
		t.Fatalf("New(4): %v", err)
	}
	want := zeroRegs(16)
	if got := e.InspectRegisters(); !regsEqual(got, want) {
		t.Fatalf("fresh registers = %v, want all zero", got)
	}

	// rho=1: top high bit set. Index 1 (low nibble 0001), high=1<<63.
	h1 := uint64(1)<<63 | 0x1
	e.Add(h1)
	want[1] = 1
	if got := e.InspectRegisters(); !regsEqual(got, want) {
		t.Fatalf("after h1: %v, want %v", got, want)
	}

	// rho=3: two leading zeros then a 1, same index 1.
	h2 := uint64(1)<<61 | 0x1
	e.Add(h2)
	want[1] = 3 // larger rho replaces
	if got := e.InspectRegisters(); !regsEqual(got, want) {
		t.Fatalf("after h2 (rho=3): %v, want %v", got, want)
	}

	// rho=2 at same index: smaller rho must NOT overwrite.
	h3 := uint64(1)<<62 | 0x1
	e.Add(h3)
	if got := e.InspectRegisters(); !regsEqual(got, want) {
		t.Fatalf("after h3 (rho=2, must keep 3): %v, want %v", got, want)
	}

	// rho upper bound for p=4 is 61: hash == index (high bits all zero),
	// index 5.
	h4 := uint64(0x5)
	e.Add(h4)
	want[5] = 61
	if got := e.InspectRegisters(); !regsEqual(got, want) {
		t.Fatalf("after h4 (all-zero high, rho=61): %v, want %v", got, want)
	}

	// Another all-zero-high hash at a different index: upper bound again.
	h5 := uint64(0xA)
	e.Add(h5)
	want[10] = 61
	if got := e.InspectRegisters(); !regsEqual(got, want) {
		t.Fatalf("after h5: %v, want %v", got, want)
	}
}

// TestSplitRhoTable checks rho arithmetic directly, including the boundary
// ranks and index extraction.
func TestSplitRhoTable(t *testing.T) {
	e, _ := New(4)
	cases := []struct {
		hash uint64
		idx  uint64
		rho  uint8
	}{
		{uint64(1) << 63, 0, 1},      // top bit, index 0
		{uint64(1)<<63 | 0xF, 15, 1}, // top bit, index 15
		{uint64(1) << 62, 0, 2},      // one leading zero
		{uint64(1) << 60, 0, 4},
		{uint64(1) << 4, 0, 60}, // last high bit position
		{0x0, 0, 61},            // all bits zero: upper bound
		{0x3, 3, 61},            // high bits all zero, index 3
	}
	for _, c := range cases {
		idx, rho := e.split(c.hash)
		if idx != c.idx || rho != c.rho {
			t.Errorf("split(%016x) = (idx=%d,rho=%d), want (idx=%d,rho=%d)",
				c.hash, idx, rho, c.idx, c.rho)
		}
	}
}

// TestInspectRegistersIsCopy mutates the returned snapshot and verifies the
// estimator's internal state is untouched.
func TestInspectRegistersIsCopy(t *testing.T) {
	e, _ := New(4)
	e.Add(uint64(1)<<63 | 0x2)
	snap := e.InspectRegisters()
	for i := range snap {
		snap[i] = 255
	}
	again := e.InspectRegisters()
	if again[2] != 1 {
		t.Fatalf("internal registers mutated through snapshot: %v", again)
	}
	for i, r := range again {
		if i != 2 && r != 0 {
			t.Fatalf("unexpected register %d = %d", i, r)
		}
	}
}

// TestNewPrecisionValidation covers every legal and illegal p.
func TestNewPrecisionValidation(t *testing.T) {
	for p := MinPrecision; p <= MaxPrecision; p++ {
		e, err := New(p)
		if err != nil {
			t.Fatalf("New(%d) unexpected error: %v", p, err)
		}
		if got := len(e.InspectRegisters()); got != 1<<p {
			t.Fatalf("New(%d) has %d registers, want %d", p, got, 1<<p)
		}
	}
	for _, p := range []int{-1, 0, 3, 17, 64, 1 << 20} {
		if _, err := New(p); !errors.Is(err, ErrInvalidPrecision) {
			t.Fatalf("New(%d) error = %v, want ErrInvalidPrecision", p, err)
		}
	}
}
