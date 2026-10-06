package logkv

import (
	"os"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T, maxSeg int64) *Store {
	t.Helper()
	s, err := Open(Config{Dir: t.TempDir(), MaxSegmentBytes: maxSeg, WriteHints: true})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return s
}

func reopen(t *testing.T, s *Store) *Store {
	t.Helper()
	dir := s.dir
	cfg := s.cfg
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	n, err := Open(cfg)
	if err != nil {
		t.Fatalf("reopen %s: %v", dir, err)
	}
	return n
}

func mustPut(t *testing.T, s *Store, key, val string) {
	t.Helper()
	if err := s.Put([]byte(key), []byte(val)); err != nil {
		t.Fatalf("put %q: %v", key, err)
	}
}

func mustDelete(t *testing.T, s *Store, key string) {
	t.Helper()
	if err := s.Delete([]byte(key)); err != nil {
		t.Fatalf("delete %q: %v", key, err)
	}
}

func mustStatus(t *testing.T, s *Store, key string, want Status) {
	t.Helper()
	_, st, err := s.Get([]byte(key))
	if err != nil {
		t.Fatalf("get %q: %v", key, err)
	}
	if st != want {
		t.Fatalf("get %q: status=%v want %v", key, st, want)
	}
}

func mustGet(t *testing.T, s *Store, key, want string) {
	t.Helper()
	val, st, err := s.Get([]byte(key))
	if err != nil {
		t.Fatalf("get %q: %v", key, err)
	}
	if st != StatusFound || string(val) != want {
		t.Fatalf("get %q: (%q,%v) want (%q,found)", key, val, st, want)
	}
}

// rotateNow 直接封口当前活动段（测试辅助，要求活动段非空）。
func rotateNow(t *testing.T, s *Store) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active.size == 0 {
		t.Fatalf("rotate on empty active segment")
	}
	if err := s.rotate(); err != nil {
		t.Fatalf("rotate: %v", err)
	}
}

func segIDs(t *testing.T, s *Store) (sealed []uint32, active uint32) {
	t.Helper()
	s.mu.RLock()
	defer s.mu.RUnlock()
	for id := range s.segments {
		sealed = append(sealed, id)
	}
	return sealed, s.active.id
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

func writeFile(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func dirFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func segPath(s *Store, id uint32) string {
	return filepath.Join(s.dir, segmentPath("", id))
}
