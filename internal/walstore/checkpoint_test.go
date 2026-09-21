package walstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckpointThenCrash(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	s.Commit(map[string]string{"a": "1", "b": "2"})
	if err := s.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	s.Commit(map[string]string{"c": "3"})
	// 崩溃：不 Close，直接重开。
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s2.Close()
	for k, v := range map[string]string{"a": "1", "b": "2", "c": "3"} {
		if got, ok := s2.Get(k); !ok || got != v {
			t.Fatalf("Get(%s) = %q,%v", k, got, ok)
		}
	}
}

func TestCheckpointWalTruncatedAfter(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	s.Commit(map[string]string{"a": "1"})
	s.Checkpoint()
	s.Commit(map[string]string{"b": "2"})
	// 检查点之后 wal.log 被截断到任意位置（含 0），数据仍完整。
	truncateWal(t, dir, readWal(t, dir), 3)
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s2.Close()
	if v, ok := s2.Get("a"); !ok || v != "1" {
		t.Fatalf("checkpoint data lost: %q,%v", v, ok)
	}
	if _, ok := s2.Get("b"); ok {
		t.Fatal("torn tail record should be dropped")
	}
}

func TestCheckpointTornTmpFile(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	s.Commit(map[string]string{"a": "1"})
	// 模拟检查点写到一半崩溃：留下残缺的临时文件。
	if err := os.WriteFile(filepath.Join(dir, tmpFileName),
		[]byte{0x57, 0x41, 0x4c}, 0o644); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s2.Close()
	if v, ok := s2.Get("a"); !ok || v != "1" {
		t.Fatalf("data lost: %q,%v", v, ok)
	}
	if _, err := os.Stat(filepath.Join(dir, tmpFileName)); !os.IsNotExist(err) {
		t.Fatal("tmp checkpoint file not cleaned up")
	}
}

func TestCheckpointContinueCommit(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	s.Commit(map[string]string{"a": "1"})
	s.Checkpoint()
	// 检查点后继续写，再崩溃恢复。
	s.Commit(map[string]string{"b": "2"})
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s2.Close()
	if s2.Len() != 2 {
		t.Fatalf("Len = %d, want 2", s2.Len())
	}
	// 再次检查点 + 截断 wal，数据仍在。
	s2.Checkpoint()
	truncateWal(t, dir, nil, 0)
	s3, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s3.Close()
	if s3.Len() != 2 {
		t.Fatalf("Len = %d, want 2", s3.Len())
	}
}
