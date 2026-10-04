package pubpool

import (
	"errors"
	"testing"
)

func TestNewInvalid(t *testing.T) {
	cases := []struct {
		a, l, h, s int
	}{
		{0, 1024, 1087, 16},
		{4097, 1024, 1087, 16},
		{2, 1023, 1087, 16},
		{2, 1024, 65536, 16},
		{2, 2000, 1024, 16},
		{2, 1024, 1087, 17},
	}
	for _, c := range cases {
		if _, err := New(c.a, c.l, c.h, c.s); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("New(%v) err=%v want invalid", c, err)
		}
	}
}

func TestGeometryAndBitmap(t *testing.T) {
	p, err := New(2, 1024, 1087, 16)
	if err != nil {
		t.Fatal(err)
	}
	if p.K() != 4 {
		t.Fatalf("K=%d", p.K())
	}
	lo, hi := p.BlockRange(1)
	if lo != 1040 || hi != 1055 {
		t.Fatalf("range=%d,%d", lo, hi)
	}
	if j := p.BlockOf(1055); j != 1 {
		t.Fatalf("block=%d", j)
	}
	if j := p.FirstFree(0); j != 0 {
		t.Fatalf("first=%d", j)
	}
	p.MarkUsed(0, 0)
	p.MarkUsed(0, 2)
	if j := p.FirstFree(0); j != 1 {
		t.Fatalf("first=%d want 1", j)
	}
	p.MarkFree(0, 0)
	if j := p.FirstFree(0); j != 0 {
		t.Fatalf("first=%d want 0", j)
	}
	if p.Drained(1) {
		t.Fatal("fresh addr drained")
	}
	if err := p.Drain(1); err != nil {
		t.Fatal(err)
	}
	if !p.Drained(1) {
		t.Fatal("drain not effective")
	}
	if err := p.Drain(1); err != nil {
		t.Fatalf("repeat drain: %v", err)
	}
	if err := p.Drain(9); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("drain oob err=%v", err)
	}
}
