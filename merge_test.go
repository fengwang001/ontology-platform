package ontology

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestMergeTakesElementWiseMax(t *testing.T) {
	a, _ := New(4)
	b, _ := New(4)
	a.Add(1<<62 | 3) // reg[3] = 2
	b.Add(1<<60 | 3) // reg[3] = 4
	b.Add(1<<61 | 9) // reg[9] = 3
	m, err := Merge(a, b)
	if err != nil {
		t.Fatal(err)
	}
	reg := m.InspectRegisters()
	if reg[3] != 4 || reg[9] != 3 {
		t.Fatalf("merged reg[3]=%d reg[9]=%d, want 4 and 3", reg[3], reg[9])
	}
}

func TestMergeWithEmptyIsIdentity(t *testing.T) {
	a, _ := New(8)
	for _, hash := range distinctHashes(3, 500) {
		a.Add(hash)
	}
	empty, _ := New(8)
	m, err := Merge(a, empty)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.InspectRegisters(), m.InspectRegisters()) {
		t.Fatal("merge with empty estimator is not identity")
	}
}

func TestMergeDoesNotModifySources(t *testing.T) {
	a, _ := New(6)
	b, _ := New(6)
	for _, hash := range distinctHashes(11, 300) {
		a.Add(hash)
	}
	for _, hash := range distinctHashes(12, 300) {
		b.Add(hash)
	}
	snapA, snapB := a.InspectRegisters(), b.InspectRegisters()
	if _, err := Merge(a, b); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(snapA, a.InspectRegisters()) {
		t.Fatal("Merge modified source a")
	}
	if !bytes.Equal(snapB, b.InspectRegisters()) {
		t.Fatal("Merge modified source b")
	}
}

func TestMergeMismatchP(t *testing.T) {
	a, _ := New(4)
	b, _ := New(9)
	_, err := Merge(a, b)
	if err == nil {
		t.Fatal("want error for mismatched p")
	}
	var pm *PMismatchError
	if !errors.As(err, &pm) {
		t.Fatalf("want *PMismatchError, got %T", err)
	}
	if pm.Pa != 4 || pm.Pb != 9 {
		t.Fatalf("PMismatchError has Pa=%d Pb=%d, want 4 and 9", pm.Pa, pm.Pb)
	}
	if msg := err.Error(); !strings.Contains(msg, "4") || !strings.Contains(msg, "9") {
		t.Fatalf("error message %q must name both p values", msg)
	}
}

func TestMergeCardinalityUnion(t *testing.T) {
	a, _ := New(14)
	b, _ := New(14)
	for _, hash := range distinctHashes(21, 50_000) {
		a.Add(hash)
	}
	for _, hash := range distinctHashes(22, 50_000) {
		b.Add(hash)
	}
	m, err := Merge(a, b)
	if err != nil {
		t.Fatal(err)
	}
	est := m.Estimate()
	diff := float64(est) - 100_000
	if diff < 0 {
		diff = -diff
	}
	if rel := diff / 100_000; rel >= 0.15 {
		t.Fatalf("merged estimate=%d, relative error %.4f >= 0.15", est, rel)
	}
}
