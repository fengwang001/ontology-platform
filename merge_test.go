package hll

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestMergeMaxPerRegister(t *testing.T) {
	const p = 6
	a, _ := New(p)
	b, _ := New(p)

	// Register 3: a stores rho 5, b stores rho 2 -> merged keeps 5.
	a.Add(makeHash(t, p, 3, uint64(1)<<(uint(64-p)-5)))
	b.Add(makeHash(t, p, 3, uint64(1)<<(uint(64-p)-2)))
	// Register 4: only b touched -> merged equals b.
	b.Add(makeHash(t, p, 4, uint64(1)<<(uint(64-p)-4)))
	// Register 5: only a touched.
	a.Add(makeHash(t, p, 5, uint64(1)<<(uint(64-p)-7)))

	m, err := Merge(a, b)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	regs := m.InspectRegisters()
	if regs[3] != 5 {
		t.Fatalf("reg[3] = %d, want max 5", regs[3])
	}
	if regs[4] != 4 {
		t.Fatalf("reg[4] = %d, want 4", regs[4])
	}
	if regs[5] != 7 {
		t.Fatalf("reg[5] = %d, want 7", regs[5])
	}

	// Equivalent to pairwise max of the two snapshots.
	ar, br := a.InspectRegisters(), b.InspectRegisters()
	for i := range regs {
		want := ar[i]
		if br[i] > want {
			want = br[i]
		}
		if regs[i] != want {
			t.Fatalf("reg[%d] = %d, want pairwise max %d", i, regs[i], want)
		}
	}
}

func TestMergeEmptyIsIdentity(t *testing.T) {
	const p = 9
	a, _ := New(p)
	for _, h := range distinctHashes(50) {
		a.Add(h)
	}
	before := a.InspectRegisters()
	empty, _ := New(p)

	m, err := Merge(a, empty)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if !slices.Equal(m.InspectRegisters(), before) {
		t.Fatal("merge with empty estimator changed registers")
	}

	m2, err := Merge(empty, a)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if !slices.Equal(m2.InspectRegisters(), before) {
		t.Fatal("merge empty-first changed registers")
	}

	bothEmpty, err := Merge(empty, empty)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if bothEmpty.Estimate() != 0 {
		t.Fatal("merge of two empties is not empty")
	}
}

func TestMergeDoesNotModifySources(t *testing.T) {
	const p = 8
	a, _ := New(p)
	b, _ := New(p)
	for _, h := range distinctHashes(30) {
		a.Add(h)
	}
	for _, h := range distinctHashes(40) {
		b.Add(h ^ 0xDEADBEEF)
	}
	aBefore, bBefore := a.InspectRegisters(), b.InspectRegisters()

	if _, err := Merge(a, b); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if !slices.Equal(a.InspectRegisters(), aBefore) {
		t.Fatal("Merge modified left source")
	}
	if !slices.Equal(b.InspectRegisters(), bBefore) {
		t.Fatal("Merge modified right source")
	}
}

func TestMergePrecisionMismatch(t *testing.T) {
	a, _ := New(4)
	b, _ := New(16)
	m, err := Merge(a, b)
	if m != nil {
		t.Fatal("expected nil result on precision mismatch")
	}
	if !errors.Is(err, ErrPrecisionMismatch) {
		t.Fatalf("err = %v, want errors.Is ErrPrecisionMismatch", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "p=4") || !strings.Contains(msg, "p=16") {
		t.Fatalf("error must name both p values, got %q", msg)
	}
}

func TestMergeNil(t *testing.T) {
	a, _ := New(8)
	if _, err := Merge(a, nil); err == nil {
		t.Fatal("Merge(a, nil) returned nil error")
	}
	if _, err := Merge(nil, a); err == nil {
		t.Fatal("Merge(nil, a) returned nil error")
	}
}
