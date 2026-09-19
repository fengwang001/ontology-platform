package ontology

import (
	"reflect"
	"testing"
)

func TestOverwriteUpdatesRanking(t *testing.T) {
	s, _ := New(3, Desc)
	s.Push("a", 10)
	s.Push("b", 8)
	s.Push("c", 6)
	s.Push("d", 4)

	if got := ids(s.Snapshot()); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("initial = %v", got)
	}

	// c improves and overtakes b.
	s.Push("c", 9)
	if got := ids(s.Snapshot()); !reflect.DeepEqual(got, []string{"a", "c", "b"}) {
		t.Fatalf("after improve = %v", got)
	}

	// a worsens below d and must leave the top K.
	s.Push("a", 1)
	// d had never been admitted, so the freed slot stays empty.
	if got := ids(s.Snapshot()); !reflect.DeepEqual(got, []string{"c", "b"}) {
		t.Fatalf("after worsen = %v", got)
	}

	// The old a entry must not linger internally either.
	if s.Len() != 2 {
		t.Fatalf("len = %d, want 2", s.Len())
	}
	for _, e := range s.Snapshot() {
		if e.ID == "a" {
			t.Fatal("duplicate/stale a present")
		}
	}
}

func TestOverwriteAscDropsOut(t *testing.T) {
	s, _ := New(2, Asc)
	s.Push("a", 1)
	s.Push("b", 2)
	s.Push("c", 3)
	if got := ids(s.Snapshot()); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("initial = %v", got)
	}
	s.Push("a", 99)
	// c had never been admitted, so the freed slot stays empty.
	if got := ids(s.Snapshot()); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("after overwrite = %v", got)
	}
}

func TestOverwriteWhenNotFull(t *testing.T) {
	s, _ := New(5, Desc)
	s.Push("a", 1)
	s.Push("a", 100)
	got := s.Snapshot()
	if len(got) != 1 || got[0].ID != "a" || got[0].Score != 100 {
		t.Fatalf("got %+v", got)
	}
}

func TestCapacitySmallerThanInput(t *testing.T) {
	s, _ := New(4, Desc)
	if got := ids(s.Snapshot()); len(got) != 0 {
		t.Fatalf("empty snapshot = %v", got)
	}
	for i := 0; i < 1000; i++ {
		s.Push(string(rune('a'+i%26))+string(rune('a'+i/26)), float64(i))
		if s.Len() > 4 {
			t.Fatalf("len exceeded K: %d after %d pushes", s.Len(), i+1)
		}
	}
	if s.Len() != 4 {
		t.Fatalf("len = %d, want 4", s.Len())
	}
}

func TestInvalidConstruction(t *testing.T) {
	for _, k := range []int{0, -1, -100} {
		s, err := New(k, Desc)
		if err == nil || s != nil {
			t.Fatalf("New(%d) must fail", k)
		}
		if !IsInvalidCapacity(err) {
			t.Fatalf("New(%d) err = %v, want ErrInvalidCapacity", k, err)
		}
	}
	if _, err := New(3, Direction(99)); err != ErrInvalidDirection {
		t.Fatalf("bad direction err = %v", err)
	}
}
