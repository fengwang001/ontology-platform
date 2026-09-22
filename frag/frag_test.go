package frag

import (
	"errors"
	"reflect"
	"testing"
)

func TestAdjacentIntervalsMerge(t *testing.T) {
	s := New(10)
	s.Add(0, []byte("hello"))
	s.Add(5, []byte("world"))
	want := []Interval{{0, 10}}
	if got := s.Intervals(); !reflect.DeepEqual(got, want) {
		t.Fatalf("intervals = %v, want %v", got, want)
	}
	if !s.Complete() {
		t.Fatal("set should be complete")
	}
	if got := string(s.Assemble()); got != "helloworld" {
		t.Fatalf("assemble = %q", got)
	}
}

func TestOverlapMergeConsistent(t *testing.T) {
	s := New(15)
	s.Add(0, []byte("0123456789"))
	dup, err := s.Check(5, []byte("56789abcde"))
	if err != nil || dup {
		t.Fatalf("Check = (%v, %v)", dup, err)
	}
	s.Add(5, []byte("56789abcde"))
	want := []Interval{{0, 15}}
	if got := s.Intervals(); !reflect.DeepEqual(got, want) {
		t.Fatalf("intervals = %v, want %v", got, want)
	}
	if got := string(s.Assemble()); got != "0123456789abcde" {
		t.Fatalf("assemble = %q", got)
	}
}

func TestConflictReportsExactRange(t *testing.T) {
	s := New(10)
	s.Add(2, []byte("abcde"))
	_, err := s.Check(0, []byte("abXYc"))
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want ConflictError", err)
	}
	if ce.Start != 2 || ce.End != 4 {
		t.Fatalf("conflict range = [%d,%d), want [2,4)", ce.Start, ce.End)
	}
	// State must be untouched by the failed check.
	want := []Interval{{2, 7}}
	if got := s.Intervals(); !reflect.DeepEqual(got, want) {
		t.Fatalf("intervals = %v, want %v", got, want)
	}
	if s.Stored() != 5 {
		t.Fatalf("stored = %d, want 5", s.Stored())
	}
}

func TestDuplicateDetection(t *testing.T) {
	s := New(10)
	s.Add(0, []byte("hello"))
	dup, err := s.Check(0, []byte("hello"))
	if !dup || err != nil {
		t.Fatalf("exact dup Check = (%v, %v)", dup, err)
	}
	// Fully covered sub-range is also idempotent.
	dup, err = s.Check(1, []byte("ell"))
	if !dup || err != nil {
		t.Fatalf("covered dup Check = (%v, %v)", dup, err)
	}
	// Same bytes but extending beyond coverage is not a duplicate.
	dup, err = s.Check(0, []byte("helloworld"))
	if dup || err != nil {
		t.Fatalf("extending Check = (%v, %v)", dup, err)
	}
}

func TestIntervalsNormalized(t *testing.T) {
	s := New(20)
	s.Add(10, []byte("aa"))
	s.Add(0, []byte("bb"))
	s.Add(5, []byte("cc"))
	s.Add(2, []byte("ddd")) // bridges [0,2) and [5,7)
	want := []Interval{{0, 7}, {10, 12}}
	if got := s.Intervals(); !reflect.DeepEqual(got, want) {
		t.Fatalf("intervals = %v, want %v", got, want)
	}
	if s.Received() != 9 {
		t.Fatalf("received = %d, want 9", s.Received())
	}
	if s.Complete() {
		t.Fatal("set must not be complete")
	}
}
