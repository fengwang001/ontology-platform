package api

import (
	"errors"
	"reflect"
	"testing"
)

func TestNewRejectsBadMemCap(t *testing.T) {
	for _, c := range []int{0, -1, -100} {
		if _, err := New(c, 8, 8); !errors.Is(err, ErrBadMemCap) {
			t.Fatalf("New(%d) err=%v, want ErrBadMemCap", c, err)
		}
	}
	if _, err := New(1, 8, 8); err != nil {
		t.Fatalf("New(1) unexpected err %v", err)
	}
}

func TestSentinelsDistinct(t *testing.T) {
	errs := []error{ErrBadMemCap, ErrEmptyKey, ErrEmptyValue, ErrKeyTooLong, ErrTooManyKeys}
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errors.Is(errs[i], errs[j]) {
				t.Fatalf("sentinels %d and %d are not distinct", i, j)
			}
		}
	}
}

func TestRejectedOpsAtomic(t *testing.T) {
	cases := []struct {
		name  string
		setup func(s *Store)
		k, v  string
		want  error
	}{
		{"empty key", nil, "", "v", ErrEmptyKey},
		{"empty value", nil, "a", "", ErrEmptyValue},
		{"key too long", nil, "toolong", "v", ErrKeyTooLong}, // maxKeyLen 4
		{"too many keys", func(s *Store) {
			_ = s.Write("a", "1")
			_ = s.Write("b", "1")
		}, "c", "1", ErrTooManyKeys}, // maxKeys 2
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := New(2, 4, 2)
			if err != nil {
				t.Fatal(err)
			}
			if c.setup != nil {
				c.setup(s)
			}
			keysBefore := s.st.HotKeys()
			totalBefore := s.st.TotalKeys()
			readsBefore := s.DiskReads()
			if err := s.Write(c.k, c.v); !errors.Is(err, c.want) {
				t.Fatalf("Write err=%v, want %v", err, c.want)
			}
			// invariant 4: memory, disk, timestamps, disk-reads all unchanged
			if got := s.st.HotKeys(); !reflect.DeepEqual(got, keysBefore) {
				t.Fatalf("hot set changed: %v -> %v", keysBefore, got)
			}
			if s.st.TotalKeys() != totalBefore {
				t.Fatalf("total keys changed %d -> %d", totalBefore, s.st.TotalKeys())
			}
			if s.DiskReads() != readsBefore {
				t.Fatalf("disk reads changed %d -> %d", readsBefore, s.DiskReads())
			}
			// still usable after rejection: a normal write and read succeed
			if err := s.Write("a", "9"); err != nil {
				t.Fatalf("store unusable after rejection: %v", err)
			}
			if got, ok := s.Read("a"); !ok || got != "9" {
				t.Fatalf("Read(a)=%q,%v after recovery, want 9,true", got, ok)
			}
		})
	}
}

func TestOverwriteNeverHitsMaxKeys(t *testing.T) {
	s, _ := New(2, 8, 2)
	if err := s.Write("a", "1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Write("a", "2"); err != nil { // same key must not count as new
		t.Fatalf("overwrite rejected: %v", err)
	}
	if got, ok := s.Read("a"); !ok || got != "2" {
		t.Fatalf("Read(a)=%q,%v want 2,true", got, ok)
	}
}

func TestSelfCheck(t *testing.T) {
	s, err := New(4, 16, 16)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
