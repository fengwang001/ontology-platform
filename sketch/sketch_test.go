package sketch

import (
	"strconv"
	"testing"
)

func feedSeq(s *Sketch, n int) map[string]uint64 {
	truth := map[string]uint64{}
	for i := 0; i < n; i++ {
		k := "key" + strconv.Itoa(i%41)
		if err := s.Add(k, 1); err != nil {
			panic(err)
		}
		truth[k]++
	}
	return truth
}

func TestNeverUnderestimate(t *testing.T) {
	s, err := New(37, 4)
	if err != nil {
		t.Fatal(err)
	}
	truth := feedSeq(s, 500)
	for k, c := range truth {
		if est, _ := s.Estimate(k); est < c {
			t.Fatalf("Estimate(%q)=%d < true %d", k, est, c)
		}
	}
	if s.Total() != 500 {
		t.Fatalf("Total()=%d, want 500", s.Total())
	}
}

func TestDeterminism(t *testing.T) {
	for _, dims := range [][2]int{{8, 2}, {64, 5}, {1000, 3}} {
		a, _ := New(dims[0], dims[1])
		b, _ := New(dims[0], dims[1])
		feedSeq(a, 200)
		feedSeq(b, 200)
		for i := range a.cells {
			if a.cells[i] != b.cells[i] {
				t.Fatalf("dims %v: cell %d differs", dims, i)
			}
		}
	}
}

func TestMergeIsomorphism(t *testing.T) {
	for _, split := range []int{0, 50, 200} {
		a, _ := New(32, 3)
		b, _ := New(32, 3)
		joint, _ := New(32, 3)
		for i := 0; i < 200; i++ {
			k := "k" + strconv.Itoa(i%29)
			joint.Add(k, 1)
			if i < split {
				a.Add(k, 1)
			} else {
				b.Add(k, 1)
			}
		}
		if err := a.Merge(b); err != nil {
			t.Fatal(err)
		}
		for i := range a.cells {
			if a.cells[i] != joint.cells[i] {
				t.Fatalf("split=%d: cell %d differs after merge", split, i)
			}
		}
		if a.Total() != joint.Total() {
			t.Fatalf("split=%d: total %d != %d", split, a.Total(), joint.Total())
		}
	}
}

func TestAccessCountPerOp(t *testing.T) {
	for _, w := range []int{1000, 100000} {
		s, _ := New(w, 5)
		s.Add("probe", 1)
		if got := s.accessed; got != 5 {
			t.Fatalf("w=%d: Add accessed %d cells, want 5", w, got)
		}
		s.Estimate("probe")
		if got := s.accessed; got != 5 {
			t.Fatalf("w=%d: Estimate accessed %d cells, want 5", w, got)
		}
	}
}

func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	if _, err := New(0, 3); err != ErrBadDimensions {
		t.Fatalf("New(0,3) err=%v", err)
	}
	if _, err := New(3, -1); err != ErrBadDimensions {
		t.Fatalf("New(3,-1) err=%v", err)
	}
	s, _ := New(16, 3)
	feedSeq(s, 50)
	before := append([]uint64(nil), s.cells...)
	total := s.Total()
	if err := s.Add("", 1); err != ErrEmptyKey {
		t.Fatalf("Add empty key err=%v", err)
	}
	if _, err := s.Estimate(""); err != ErrEmptyKey {
		t.Fatalf("Estimate empty key err=%v", err)
	}
	other, _ := New(17, 3)
	otherBefore := append([]uint64(nil), other.cells...)
	if err := s.Merge(other); err != ErrIncompatible {
		t.Fatalf("Merge err=%v", err)
	}
	for i := range s.cells {
		if s.cells[i] != before[i] {
			t.Fatalf("cell %d changed by rejected ops", i)
		}
	}
	for i := range otherBefore {
		if other.cells[i] != otherBefore[i] {
			t.Fatalf("other cell %d changed by rejected merge", i)
		}
	}
	if s.Total() != total {
		t.Fatalf("total changed by rejected ops")
	}
}

func TestSelfCheck(t *testing.T) {
	s, _ := New(16, 3)
	feedSeq(s, 100)
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("healthy sketch failed self-check: %v", err)
	}
	s.cells[0]++
	if err := s.SelfCheck(); err != ErrCorrupt {
		t.Fatalf("corrupt sketch: err=%v, want ErrCorrupt", err)
	}
}
