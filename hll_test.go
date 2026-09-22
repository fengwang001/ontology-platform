package ontology

import (
	"errors"
	"testing"
)

func TestNewRejectsInvalidP(t *testing.T) {
	for _, p := range []uint8{0, 1, 3, 17, 100, 255} {
		if _, err := New(p); !errors.Is(err, ErrInvalidP) {
			t.Fatalf("New(%d): want ErrInvalidP, got %v", p, err)
		}
	}
	for p := uint8(MinP); p <= MaxP; p++ {
		h, err := New(p)
		if err != nil {
			t.Fatalf("New(%d): unexpected error %v", p, err)
		}
		if got := len(h.InspectRegisters()); got != 1<<p {
			t.Fatalf("New(%d): want %d registers, got %d", p, 1<<p, got)
		}
	}
}

// TestRegisterSemantics hand-crafts bit patterns and asserts exact
// register values after every Add. p=4, so the low 4 bits pick the
// register and rho comes from the top 60 bits.
func TestRegisterSemantics(t *testing.T) {
	h, err := New(4)
	if err != nil {
		t.Fatal(err)
	}
	reg := func(i int) uint8 { return h.InspectRegisters()[i] }

	// Register 3, top bit at position 62: rho = 63-62+1 = 2.
	h.Add(1<<62 | 3)
	if got := reg(3); got != 2 {
		t.Fatalf("after rho=2 add: reg[3]=%d, want 2", got)
	}

	// Same register, smaller rho (top bit 63 => rho=1): no change.
	h.Add(1<<63 | 3)
	if got := reg(3); got != 2 {
		t.Fatalf("smaller rho must not overwrite: reg[3]=%d, want 2", got)
	}

	// Same register, larger rho (top bit 60 => rho=4): exact replace.
	h.Add(1<<60 | 3)
	if got := reg(3); got != 4 {
		t.Fatalf("larger rho must replace: reg[3]=%d, want 4", got)
	}

	// All remaining bits zero: rho hits the 64-p+1 = 61 ceiling.
	h.Add(3)
	if got := reg(3); got != 61 {
		t.Fatalf("rho ceiling: reg[3]=%d, want 61", got)
	}

	// A different register is untouched; everything else stays zero.
	if got := reg(7); got != 0 {
		t.Fatalf("reg[7]=%d, want 0", got)
	}
	snap := h.InspectRegisters()
	nonZero := 0
	for _, v := range snap {
		if v != 0 {
			nonZero++
		}
	}
	if nonZero != 1 {
		t.Fatalf("want exactly 1 non-zero register, got %d", nonZero)
	}
}

func TestRhoCeilingOnZeroHash(t *testing.T) {
	h, _ := New(10)
	h.Add(0) // index 0, remaining bits all zero
	if got := h.InspectRegisters()[0]; got != 64-10+1 {
		t.Fatalf("zero hash: reg[0]=%d, want %d", got, 64-10+1)
	}
}

func TestInspectRegistersReturnsCopy(t *testing.T) {
	h, _ := New(4)
	h.Add(42)
	snap := h.InspectRegisters()
	for i := range snap {
		snap[i] = 255
	}
	for i, v := range h.InspectRegisters() {
		if uint32(i) == h.index(42) {
			continue
		}
		if v != 0 {
			t.Fatalf("mutating snapshot leaked into estimator at reg %d", i)
		}
	}
}
