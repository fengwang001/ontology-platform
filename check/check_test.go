package check

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func TestNewStoreInvalidDir(t *testing.T) {
	for _, dir := range []string{"", "   ", "\t"} {
		if _, err := NewStore(dir); !errors.Is(err, ErrInvalidDir) {
			t.Fatalf("dir=%q: want ErrInvalidDir, got %v", dir, err)
		}
	}
}

func TestPersistRestoreRoundTrip(t *testing.T) {
	s := newTestStore(t)
	for _, v := range []int64{0, 1, 2, 4, 6, 1 << 40} {
		if err := s.Persist(v); err != nil {
			t.Fatalf("Persist(%d): %v", v, err)
		}
		got, err := s.Restore()
		if err != nil || got != v || s.recordsRead != 1 {
			t.Fatalf("v=%d: got %d records=%d err=%v", v, got, s.recordsRead, err)
		}
	}
	if _, err := os.Stat(filepath.Join(s.dir, tmpName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp file leaked: %v", err)
	}
}

func TestRestoreMissingFile(t *testing.T) {
	s := newTestStore(t)
	v, err := s.Restore()
	if err != nil || v != 0 || s.recordsRead != 0 {
		t.Fatalf("got v=%d records=%d err=%v", v, s.recordsRead, err)
	}
}

func TestRestoreCorrupt(t *testing.T) {
	cases := map[string][]byte{
		"short":    {1, 2, 3},
		"seven":    make([]byte, 7),
		"nine":     make([]byte, 9),
		"negative": bytes.Repeat([]byte{0xff}, 8), // uint64 max == int64 -1
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			s := newTestStore(t)
			p := filepath.Join(s.dir, FileName)
			if err := os.WriteFile(p, b, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Restore(); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("want ErrCorrupt, got %v", err)
			}
		})
	}
}

func TestPersistFaultLeavesNoTrace(t *testing.T) {
	s := newTestStore(t)
	if err := s.Persist(5); err != nil {
		t.Fatal(err)
	}
	s.SetPersistFault(true)
	if err := s.Persist(6); !errors.Is(err, ErrPersistFailed) {
		t.Fatalf("want ErrPersistFailed, got %v", err)
	}
	s.SetPersistFault(false)
	got, err := s.Restore()
	if err != nil || got != 5 {
		t.Fatalf("state changed after fault: next=%d err=%v", got, err)
	}
}

// TestRestoreRecordCountConstant proves the checkpoint is one int64 scalar,
// not an append-only log: after m allocations Restore parses exactly 1
// record regardless of m (an O(m) log would report m).
func TestRestoreRecordCountConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := newTestStore(t)
		for i := 1; i <= m; i++ {
			if err := s.Persist(int64(i)); err != nil {
				t.Fatalf("m=%d i=%d: %v", m, i, err)
			}
		}
		v, err := s.Restore()
		if err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		if v != int64(m) || s.recordsRead != 1 {
			t.Fatalf("m=%d: next=%d recordsRead=%d, want %d/1", m, v, s.recordsRead, m)
		}
	}
}
