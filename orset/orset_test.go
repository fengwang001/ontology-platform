package orset

import (
	"errors"
	"fmt"
	"testing"
)

// The checked counter must not depend on the number of other elements:
// lookups index adds by element, never scan the whole add set.
func TestIndexedLookup(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := New(0, m+10)
		for i := 0; i < m; i++ {
			if err := s.Add(fmt.Sprintf("e%d", i)); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Add("target"); err != nil {
			t.Fatal(err)
		}
		if err := s.Add("target"); err != nil { // second tag on the same element
			t.Fatal(err)
		}
		s.Contains("target")
		if s.checked > 2 {
			t.Fatalf("m=%d: Contains inspected %d entries, want <= 2", m, s.checked)
		}
		if err := s.Remove("target"); err != nil {
			t.Fatal(err)
		}
		if s.checked > 2 {
			t.Fatalf("m=%d: Remove inspected %d entries, want <= 2", m, s.checked)
		}
		s.Contains("missing")
		if s.checked != 0 {
			t.Fatalf("m=%d: missing element inspected %d entries", m, s.checked)
		}
	}
}

func TestMergeLaws(t *testing.T) {
	mk := func(id int, adds ...string) func() *Set {
		return func() *Set {
			s := New(id, 100)
			for _, e := range adds {
				_ = s.Add(e)
			}
			return s
		}
	}
	x, z := mk(0, "a", "b"), mk(2, "c")
	y := func() *Set { s := mk(1, "b", "c")(); _ = s.Remove("b"); return s }
	merge := func(s *Set, o ...*Set) *Set {
		for _, other := range o {
			if err := s.MergeFrom(other); err != nil {
				t.Fatal(err)
			}
		}
		return s
	}
	self := x()
	if err := self.MergeFrom(self); err != nil {
		t.Fatal(err)
	}
	laws := []struct {
		name string
		a, b *Set
	}{
		{"commutative", merge(x(), y()), merge(y(), x())},
		{"associative", merge(x(), y(), z()), merge(x(), merge(y(), z()))},
		{"idempotent", merge(x(), x()), x()},
		{"self-merge", self, x()},
		{"absorptive", merge(merge(x(), y()), y()), merge(x(), y())},
	}
	for _, law := range laws {
		if !law.a.Snapshot().Equal(law.b.Snapshot()) {
			t.Errorf("%s violated", law.name)
		}
	}
}

func TestAddWins(t *testing.T) {
	a, b := New(0, 10), New(1, 10)
	_ = a.Add("e")
	_ = b.MergeFrom(a)
	_ = b.Remove("e") // observes only tag (0,1)
	_ = a.Add("e")    // concurrent tag (0,2), never observed
	_ = a.MergeFrom(b)
	_ = b.MergeFrom(a)
	for i, s := range []*Set{a, b} {
		if !s.Contains("e") {
			t.Fatalf("replica %d lost the unobserved add", i)
		}
	}
}

func TestRejectNoSideEffect(t *testing.T) {
	s := New(0, 2)
	_ = s.Add("e")
	if err := s.Add(""); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty add: %v", err)
	}
	_ = s.Add("x") // must get S=2: the rejected add consumed no sequence number
	if ts := s.Snapshot().Adds["x"]; len(ts) != 1 || ts[0].S != 2 {
		t.Fatal("tag counter skipped by rejected op")
	}
	before := s.Snapshot()
	if err := s.Remove("zz"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing remove: %v", err)
	}
	if err := s.Add("f"); !errors.Is(err, ErrCapacity) {
		t.Fatalf("overflow add: %v", err)
	}
	if !before.Equal(s.Snapshot()) || s.seq != 2 {
		t.Fatal("rejected op changed state")
	}
}
