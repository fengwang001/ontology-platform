package walstore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func openTempStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s, dir
}

func reopen(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	return s
}

func walSize(t *testing.T, dir string) int64 {
	t.Helper()
	fi, err := os.Stat(filepath.Join(dir, walFileName))
	if err != nil {
		t.Fatalf("stat wal: %v", err)
	}
	return fi.Size()
}

func TestOpenEmptyDir(t *testing.T) {
	s, _ := openTempStore(t)
	defer s.Close()
	if s.Len() != 0 {
		t.Fatalf("empty store Len = %d, want 0", s.Len())
	}
	if _, ok := s.Get("missing"); ok {
		t.Fatal("missing key reported present")
	}
}

func TestCommitAndGet(t *testing.T) {
	s, dir := openTempStore(t)
	if err := s.Commit(map[string]string{"a": "1", "b": "2"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(map[string]string{"a": "9"}); err != nil {
		t.Fatal(err)
	}
	if v, ok := s.Get("a"); !ok || v != "9" {
		t.Fatalf("a = %q,%v want 9,true", v, ok)
	}
	if v, ok := s.Get("b"); !ok || v != "2" {
		t.Fatalf("b = %q,%v want 2,true", v, ok)
	}
	if s.Len() != 2 {
		t.Fatalf("Len = %d, want 2", s.Len())
	}
	s.Close()

	s = reopen(t, dir)
	defer s.Close()
	if v, ok := s.Get("a"); !ok || v != "9" {
		t.Fatalf("after reopen a = %q,%v", v, ok)
	}
}

func TestEmptyValueDistinguishedFromMissing(t *testing.T) {
	s, dir := openTempStore(t)
	if err := s.Commit(map[string]string{"k": ""}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = reopen(t, dir)
	defer s.Close()
	if v, ok := s.Get("k"); !ok || v != "" {
		t.Fatalf("empty value: got %q,%v", v, ok)
	}
	if _, ok := s.Get("nope"); ok {
		t.Fatal("missing key present")
	}
}

func TestInvalidBatches(t *testing.T) {
	s, dir := openTempStore(t)
	defer s.Close()
	sizeBefore := walSize(t, dir)
	if err := s.Commit(map[string]string{}); !errors.Is(err, ErrEmptyBatch) {
		t.Fatalf("empty batch err = %v, want ErrEmptyBatch", err)
	}
	if err := s.Commit(map[string]string{"": "v"}); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("empty key err = %v, want ErrEmptyKey", err)
	}
	if got := walSize(t, dir); got != sizeBefore {
		t.Fatalf("invalid batch wrote %d bytes", got-sizeBefore)
	}
}

func TestCheckpointSurvivesRestart(t *testing.T) {
	s, dir := openTempStore(t)
	if err := s.Commit(map[string]string{"x": "1", "y": "2"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if sz := walSize(t, dir); sz != 0 {
		t.Fatalf("wal size after checkpoint = %d, want 0", sz)
	}
	s.Close()

	s = reopen(t, dir)
	defer s.Close()
	if v, ok := s.Get("x"); s.Len() != 2 || !ok || v != "1" {
		t.Fatalf("after checkpoint+reopen: %v", s.data)
	}
	if err := s.Commit(map[string]string{"z": "3"}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s = reopen(t, dir)
	defer s.Close()
	if s.Len() != 3 {
		t.Fatalf("Len = %d, want 3", s.Len())
	}
}
