package slot

import (
	"reflect"
	"testing"
)

func TestSlot(t *testing.T) {
	s := New(3)
	if s.Free() != 3 || s.InFlight() != 0 {
		t.Fatalf("empty: Free=%d InFlight=%d", s.Free(), s.InFlight())
	}
	s.Acquire("b", 100)
	s.Acquire("a", 100) // same deadline: id breaks the tie
	s.Acquire("c", 50)
	if s.Free() != 0 || s.InFlight() != 3 {
		t.Fatalf("full: Free=%d InFlight=%d", s.Free(), s.InFlight())
	}

	got := s.Expire(100)
	want := []Entry{{ID: "c", DL: 50}, {ID: "a", DL: 100}, {ID: "b", DL: 100}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Expire(100) = %v, want %v", got, want)
	}
	if s.InFlight() != 0 {
		t.Fatalf("InFlight after expire = %d", s.InFlight())
	}

	// Rollback: restored entries behave exactly as before.
	for _, e := range want {
		s.Restore(e)
	}
	if dl, ok := s.Deadline("a"); !ok || dl != 100 {
		t.Fatalf("Deadline(a) = %d,%v", dl, ok)
	}
	s.Release("a")
	if _, ok := s.Deadline("a"); ok {
		t.Fatal("Deadline(a) after Release")
	}
	s.Release("a") // absent release is a no-op
	if s.InFlight() != 2 {
		t.Fatalf("InFlight = %d, want 2", s.InFlight())
	}

	// Partial expiry: only entries with dl <= now are popped.
	if got := s.Expire(75); !reflect.DeepEqual(got, []Entry{{ID: "c", DL: 50}}) {
		t.Fatalf("Expire(75) = %v", got)
	}
	if got := s.Expire(99); got != nil {
		t.Fatalf("Expire(99) = %v, want nil", got)
	}
	if got := s.Expire(100); !reflect.DeepEqual(got, []Entry{{ID: "b", DL: 100}}) {
		t.Fatalf("Expire(100) = %v", got)
	}
}
