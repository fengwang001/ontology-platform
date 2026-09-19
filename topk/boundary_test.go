package topk

import (
	"reflect"
	"testing"
)

func TestTieBoundaryDesc(t *testing.T) {
	s, err := New(2, Desc)
	if err != nil {
		t.Fatal(err)
	}
	s.Push("hi", 10.0)
	s.Push("omega", 5.0)
	s.Push("alpha", 5.0)
	got := ids(s.Snapshot())
	want := []string{"hi", "alpha"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Desc boundary = %v, want %v", got, want)
	}
}

func TestTieBoundaryAsc(t *testing.T) {
	s, err := New(2, Asc)
	if err != nil {
		t.Fatal(err)
	}
	s.Push("lo", -10.0)
	s.Push("omega", 5.0)
	s.Push("alpha", 5.0)
	got := ids(s.Snapshot())
	want := []string{"lo", "alpha"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Asc boundary = %v, want %v", got, want)
	}
}

func TestBoundaryNotFirstComeFirstServed(t *testing.T) {
	// Arrival order reversed vs TestTieBoundaryDesc: same result.
	s, err := New(2, Desc)
	if err != nil {
		t.Fatal(err)
	}
	s.Push("alpha", 5.0)
	s.Push("omega", 5.0)
	s.Push("hi", 10.0)
	got := ids(s.Snapshot())
	want := []string{"hi", "alpha"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reversed arrival = %v, want %v", got, want)
	}
}
