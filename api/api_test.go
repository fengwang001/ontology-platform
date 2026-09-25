package api

import (
	"errors"
	"testing"
)

func TestSelfCheck(t *testing.T) {
	a, err := New(3)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestNaiveConsistency(t *testing.T) {
	cases := []struct {
		seed      int64
		numFrames int
		ops       int
	}{
		{11, 1, 300},
		{12, 2, 300},
		{13, 3, 500},
		{14, 5, 500},
		{15, 9, 800},
	}
	for _, c := range cases {
		if err := checkRandom(c.seed, c.numFrames, c.ops); err != nil {
			t.Fatalf("seed=%d numFrames=%d: %v", c.seed, c.numFrames, err)
		}
	}
}

func TestErrorsDistinct(t *testing.T) {
	errs := []error{ErrBadFrame, ErrNotPinned, ErrPoolFull}
	for i, a := range errs {
		for j, b := range errs {
			if i != j && errors.Is(a, b) {
				t.Fatalf("errors %v and %v must be distinct", a, b)
			}
		}
	}
}

func TestNewValidation(t *testing.T) {
	for _, n := range []int{0, -1, -100} {
		if _, err := New(n); err == nil {
			t.Fatalf("New(%d) must fail", n)
		}
	}
}
