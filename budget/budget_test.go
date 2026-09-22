package budget

import (
	"errors"
	"testing"
)

func TestReserveUpToLimit(t *testing.T) {
	b := New(10)
	if err := b.Reserve(6); err != nil {
		t.Fatal(err)
	}
	if err := b.Reserve(4); err != nil {
		t.Fatal(err)
	}
	if b.Used() != 10 {
		t.Fatalf("used %d", b.Used())
	}
}

func TestReserveOverLimitFailsWithoutChange(t *testing.T) {
	b := New(10)
	if err := b.Reserve(7); err != nil {
		t.Fatal(err)
	}
	if err := b.Reserve(4); !errors.Is(err, ErrExceeded) {
		t.Fatalf("expected ErrExceeded, got %v", err)
	}
	if b.Used() != 7 {
		t.Fatalf("usage changed to %d after rejection", b.Used())
	}
}

func TestRelease(t *testing.T) {
	b := New(10)
	if err := b.Reserve(10); err != nil {
		t.Fatal(err)
	}
	b.Release(4)
	if b.Used() != 6 {
		t.Fatalf("used %d", b.Used())
	}
	if err := b.Reserve(4); err != nil {
		t.Fatal(err)
	}
}
