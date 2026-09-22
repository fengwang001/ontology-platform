package hll

import (
	"slices"
	"testing"
)

// TestRegisterBitPatterns checks register updates against hand-built bit
// patterns at p=4: initial write, immunity to a smaller rho, exact replacement
// by a larger rho, and the all-zero-remainder saturation cap.
func TestRegisterBitPatterns(t *testing.T) {
	const p = 4
	e, err := New(p)
	if err != nil {
		t.Fatalf("New(%d): %v", p, err)
	}

	// h1: register 5, 60-bit remainder = 1<<57 = 0b001 followed by 57 zeros,
	// i.e. two leading zero bits -> rho 3.
	h1 := makeHash(t, p, 5, uint64(1)<<57)
	e.Add(h1)
	if got := e.InspectRegisters()[5]; got != 3 {
		t.Fatalf("after first hit reg[5] = %d, want 3", got)
	}

	// A smaller rho (rho 1) hitting the same register changes nothing.
	e.Add(makeHash(t, p, 5, uint64(1)<<59))
	if got := e.InspectRegisters()[5]; got != 3 {
		t.Fatalf("smaller rho changed reg[5]: %d, want 3", got)
	}

	// An equal rho also changes nothing.
	e.Add(makeHash(t, p, 5, uint64(1)<<57))
	if got := e.InspectRegisters()[5]; got != 3 {
		t.Fatalf("equal rho changed reg[5]: %d, want 3", got)
	}

	// A larger rho (rho 10) replaces the stored value exactly.
	e.Add(makeHash(t, p, 5, uint64(1)<<50))
	if got := e.InspectRegisters()[5]; got != 10 {
		t.Fatalf("larger rho: reg[5] = %d, want 10", got)
	}

	// All-zero remainder saturates rho at 64-p+1 = 61, the upper bound.
	e.Add(makeHash(t, p, 5, 0))
	if got := e.InspectRegisters()[5]; got != 61 {
		t.Fatalf("saturated hit: reg[5] = %d, want 61", got)
	}

	// Nothing can exceed the saturated value.
	e.Add(makeHash(t, p, 5, 0))
	e.Add(makeHash(t, p, 5, ^uint64(0)>>(64-p)))
	if got := e.InspectRegisters()[5]; got != 61 {
		t.Fatalf("post-saturation reg[5] = %d, want 61", got)
	}

	// Every other register must still be zero.
	regs := e.InspectRegisters()
	for i, v := range regs {
		if i != 5 && v != 0 {
			t.Fatalf("untouched reg[%d] = %d, want 0", i, v)
		}
	}
}

// TestRhoTable pins rho for hand-selected patterns at p=4 and p=16.
func TestRhoTable(t *testing.T) {
	cases := []struct {
		p    uint8
		h    uint64
		want uint8
	}{
		{4, 0, 61},                // remainder all zero -> 64-4+1
		{4, uint64(1) << 63, 1},   // top bit set
		{4, uint64(1) << 62, 2},   // exactly one leading zero
		{16, 0, 49},               // 64-16+1
		{16, uint64(1) << 63, 1},  // top bit set
		{16, uint64(1) << 48, 16}, // 15 leading zeros
	}
	for _, c := range cases {
		if got := rho(c.p, c.h); got != c.want {
			t.Errorf("rho(p=%d, h=%#016x) = %d, want %d", c.p, c.h, got, c.want)
		}
	}
}

// TestInspectIsACopy proves mutating the snapshot never reaches the estimator.
func TestInspectIsACopy(t *testing.T) {
	e, _ := New(8)
	e.Add(makeHash(t, 8, 7, 1))
	snap := e.InspectRegisters()
	snap[0] = 255
	snap[7] = 255
	again := e.InspectRegisters()
	if again[0] != 0 || again[7] == 255 {
		t.Fatalf("snapshot mutation leaked into estimator: %v", again)
	}
	if !slices.Equal(e.InspectRegisters(), again) {
		t.Fatal("repeated inspections diverged")
	}
}
