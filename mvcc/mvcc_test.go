package mvcc

import (
	"errors"
	"strconv"
	"testing"
)

// TestReadCostConstant proves a read inspects O(1) versions no matter how
// many versions were committed for the key: only the latest is kept.
func TestReadCostConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 5000, 10000} {
		t.Run(strconv.Itoa(m), func(t *testing.T) {
			s := New()
			for i := 0; i < m; i++ {
				tx := s.Begin()
				if err := s.Write(tx, "k", "v"+strconv.Itoa(i)); err != nil {
					t.Fatal(err)
				}
				if err := s.Commit(tx); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.Read("k")
			if err != nil {
				t.Fatal(err)
			}
			if want := "v" + strconv.Itoa(m-1); got != want {
				t.Fatalf("m=%d: Read=%q want %q", m, got, want)
			}
			if n := s.checked.Load(); n > maxVersionsPerRead {
				t.Fatalf("m=%d: read inspected %d versions, want <= %d",
					m, n, maxVersionsPerRead)
			}
		})
	}
}

// TestRejectedOpsLeaveState pins invariant 4: every rejected operation
// fails with its own sentinel and changes nothing (C, pending, committed).
func TestRejectedOpsLeaveState(t *testing.T) {
	// The four failure classes must be mutually distinguishable.
	classes := []error{ErrEmptyKey, ErrEmptyValue, ErrTxNotBegun, ErrTxCommitted}
	for i, a := range classes {
		for _, b := range classes[i+1:] {
			if a == b {
				t.Fatalf("sentinels not distinct: %v", a)
			}
		}
	}

	s := New()
	done := s.Begin()
	if err := s.Write(done, "a", "1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(done); err != nil {
		t.Fatal(err)
	}
	live := s.Begin()
	if err := s.Write(live, "b", "2"); err != nil {
		t.Fatal(err)
	}
	c0, pend0 := s.c, len(s.pending[live])

	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"write empty key", func() error { return s.Write(live, "", "v") }, ErrEmptyKey},
		{"write empty value", func() error { return s.Write(live, "k", "") }, ErrEmptyValue},
		{"write unknown tx", func() error { return s.Write(9999, "k", "v") }, ErrTxNotBegun},
		{"commit unknown tx", func() error { return s.Commit(9999) }, ErrTxNotBegun},
		{"readtx unknown tx", func() error { _, e := s.ReadTx(9999, "a"); return e }, ErrTxNotBegun},
		{"write committed tx", func() error { return s.Write(done, "k", "v") }, ErrTxCommitted},
		{"recommit committed tx", func() error { return s.Commit(done) }, ErrTxCommitted},
		{"readtx committed tx", func() error { _, e := s.ReadTx(done, "a"); return e }, ErrTxCommitted},
		{"read empty key", func() error { _, e := s.Read(""); return e }, ErrEmptyKey},
	}
	for _, tc := range cases {
		if err := tc.op(); !errors.Is(err, tc.want) {
			t.Fatalf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}

	if s.c != c0 {
		t.Fatalf("commit counter changed: %d -> %d", c0, s.c)
	}
	if len(s.pending[live]) != pend0 {
		t.Fatalf("pending set changed by rejections")
	}
	if v, _ := s.Read("a"); v != "1" {
		t.Fatalf("committed version changed: %q", v)
	}
	if _, err := s.Read("b"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("uncommitted write leaked: %v", err)
	}
	// Store stays fully usable afterwards.
	if err := s.Commit(live); err != nil {
		t.Fatalf("store unusable after rejections: %v", err)
	}
	if v, _ := s.Read("b"); v != "2" {
		t.Fatalf("commit after rejections lost writes: %q", v)
	}
}
