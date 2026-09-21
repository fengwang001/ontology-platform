package walstore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s, dir
}

func TestOpenEmptyDir(t *testing.T) {
	s, _ := openTemp(t)
	defer s.Close()
	if s.Len() != 0 {
		t.Fatalf("Len = %d, want 0", s.Len())
	}
	if _, ok := s.Get("nope"); ok {
		t.Fatal("Get on empty store returned ok")
	}
}

func TestCommitGetLen(t *testing.T) {
	s, _ := openTemp(t)
	defer s.Close()
	if err := s.Commit(map[string]string{"a": "1", "b": "2"}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if v, ok := s.Get("a"); !ok || v != "1" {
		t.Fatalf("Get(a) = %q,%v", v, ok)
	}
	if s.Len() != 2 {
		t.Fatalf("Len = %d, want 2", s.Len())
	}
}

func TestEmptyBatchRejected(t *testing.T) {
	s, dir := openTemp(t)
	defer s.Close()
	if err := s.Commit(map[string]string{}); !errors.Is(err, ErrEmptyBatch) {
		t.Fatalf("err = %v, want ErrEmptyBatch", err)
	}
	// 空批次不得写入任何记录。
	info, err := os.Stat(filepath.Join(dir, walFileName))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("wal size = %d, want 0", info.Size())
	}
}

func TestEmptyKeyRejected(t *testing.T) {
	s, _ := openTemp(t)
	defer s.Close()
	if err := s.Commit(map[string]string{"": "v"}); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("err = %v, want ErrEmptyKey", err)
	}
}

func TestEmptyValueDistinctFromMissing(t *testing.T) {
	s, _ := openTemp(t)
	defer s.Close()
	if err := s.Commit(map[string]string{"k": ""}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	v, ok := s.Get("k")
	if !ok || v != "" {
		t.Fatalf("Get(k) = %q,%v, want \"\",true", v, ok)
	}
	if _, ok := s.Get("missing"); ok {
		t.Fatal("missing key reported present")
	}
}

func TestOverwriteOrder(t *testing.T) {
	s, _ := openTemp(t)
	defer s.Close()
	s.Commit(map[string]string{"a": "1"})
	s.Commit(map[string]string{"a": "2"})
	if v, _ := s.Get("a"); v != "2" {
		t.Fatalf("Get(a) = %q, want 2", v)
	}
	if s.Len() != 1 {
		t.Fatalf("Len = %d, want 1", s.Len())
	}
}

func TestCommitAfterClose(t *testing.T) {
	s, _ := openTemp(t)
	s.Close()
	if err := s.Commit(map[string]string{"a": "1"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestCloseReopenPersistence(t *testing.T) {
	s, dir := openTemp(t)
	s.Commit(map[string]string{"x": "9", "y": ""})
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if v, ok := s2.Get("x"); !ok || v != "9" {
		t.Fatalf("Get(x) = %q,%v", v, ok)
	}
	if v, ok := s2.Get("y"); !ok || v != "" {
		t.Fatalf("Get(y) = %q,%v", v, ok)
	}
}
