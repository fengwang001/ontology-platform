package walstore

import (
	"os"
	"path/filepath"
	"testing"
)

func walPath(dir string) string { return filepath.Join(dir, walFileName) }

func readWal(t *testing.T, dir string) []byte {
	t.Helper()
	b, err := os.ReadFile(walPath(dir))
	if err != nil {
		t.Fatalf("read wal: %v", err)
	}
	return b
}

// truncateWal 把 wal.log 截断/改写为 full[:n]，模拟断电时写了一半。
func truncateWal(t *testing.T, dir string, full []byte, n int) {
	t.Helper()
	if err := os.WriteFile(walPath(dir), full[:n], 0o644); err != nil {
		t.Fatalf("truncate wal: %v", err)
	}
}

// batchVisibility 返回批次中"键存在且值正确"的个数。
func batchVisibility(s *Store, batch map[string]string) int {
	n := 0
	for k, v := range batch {
		if got, ok := s.Get(k); ok && got == v {
			n++
		}
	}
	return n
}

// TestByteByByteTruncation 穷举：把 wal.log 从 0 到完整长度逐一
// 截断，每次都重新 Open，断言任意批次要么全部可见要么全不可见，
// 且可见批次构成已提交序列的前缀。
func TestByteByByteTruncation(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// 各批次键互不重叠，便于精确判定可见性。
	batches := []map[string]string{
		{"a": "1", "b": "2"},
		{"c": "3"},
		{"d": "4", "e": "5", "f": ""},
	}
	for _, b := range batches {
		if err := s.Commit(b); err != nil {
			t.Fatalf("Commit: %v", err)
		}
	}
	full := readWal(t, dir)
	// 不调用 Close，直接丢弃，模拟崩溃。

	for cut := 0; cut <= len(full); cut++ {
		truncateWal(t, dir, full, cut)
		s2, err := Open(dir)
		if err != nil {
			t.Fatalf("cut=%d: Open: %v", cut, err)
		}
		prefix := true
		seenMissing := false
		totalKeys := 0
		for i, b := range batches {
			vis := batchVisibility(s2, b)
			if vis != 0 && vis != len(b) {
				t.Fatalf("cut=%d: batch %d partially visible (%d/%d)",
					cut, i, vis, len(b))
			}
			if vis == 0 {
				seenMissing = true
			} else {
				if seenMissing {
					t.Fatalf("cut=%d: batch %d visible but earlier batch missing", cut, i)
				}
				totalKeys += len(b)
			}
		}
		if !prefix {
			t.Fatalf("cut=%d: visible batches not a prefix", cut)
		}
		if s2.Len() != totalKeys {
			t.Fatalf("cut=%d: Len = %d, want %d", cut, s2.Len(), totalKeys)
		}
		if err := s2.Close(); err != nil {
			t.Fatalf("cut=%d: Close: %v", cut, err)
		}
	}
}

// TestAckedBatchesDurable 已确认即持久：截断点不早于某批次，
// 则该批次恢复后必须完整可见。
func TestAckedBatchesDurable(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	b1 := map[string]string{"a": "1"}
	b2 := map[string]string{"b": "2"}
	b3 := map[string]string{"c": "3"}
	s.Commit(b1)
	s.Commit(b2)
	lenAfterB2 := len(readWal(t, dir))
	s.Commit(b3)
	full := readWal(t, dir)

	truncateWal(t, dir, full, lenAfterB2)
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s2.Close()
	if batchVisibility(s2, b1) != 1 || batchVisibility(s2, b2) != 1 {
		t.Fatal("acked batches lost after truncation")
	}
	if batchVisibility(s2, b3) != 0 {
		t.Fatal("truncated batch should not be visible")
	}
}

// TestTailGarbageThenContinue 尾部垃圾被丢弃后，日志可继续追加，
// 且再次崩溃恢复后新旧数据都正确。
func TestTailGarbageThenContinue(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	s.Commit(map[string]string{"old": "1"})
	full := readWal(t, dir)

	// 末尾接半条记录（合法头部残片 + 随机字节）。
	garbled := append(append([]byte{}, full...), 0x57, 0x41, 0x4c, 0x31, 0x00, 0x00)
	if err := os.WriteFile(walPath(dir), garbled, 0o644); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("Open with garbage tail: %v", err)
	}
	if v, ok := s2.Get("old"); !ok || v != "1" {
		t.Fatalf("old data lost: %q,%v", v, ok)
	}
	// 恢复后继续提交。
	if err := s2.Commit(map[string]string{"new": "2"}); err != nil {
		t.Fatalf("Commit after recovery: %v", err)
	}
	s2.Close()

	// 再崩溃（尾部又带垃圾）再恢复。
	full2 := readWal(t, dir)
	truncateWal(t, dir, append(full2, 0xDE, 0xAD, 0xBE), len(full2)+2)
	s3, err := Open(dir)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer s3.Close()
	if v, _ := s3.Get("old"); v != "1" {
		t.Fatalf("old = %q", v)
	}
	if v, _ := s3.Get("new"); v != "2" {
		t.Fatalf("new = %q", v)
	}
	if s3.Len() != 2 {
		t.Fatalf("Len = %d, want 2", s3.Len())
	}
}
