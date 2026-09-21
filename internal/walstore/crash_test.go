package walstore

import (
	"os"
	"path/filepath"
	"testing"
)

// batches 使用互不相交的键，便于检查"整批可见或整批不可见"。
var batches = []map[string]string{
	{"b0k1": "v01", "b0k2": "v02", "b0k3": ""}, // 含空值
	{"b1k1": "longer-value-1", "b1k2": "longer-value-2"},
	{"b2k1": "x", "b2k2": "y", "b2k3": "z", "b2k4": "w"},
	{"b3k1": "final"},
}

func writeBatches(t *testing.T, dir string, closeIt bool) int64 {
	t.Helper()
	s := reopen(t, dir)
	for _, b := range batches {
		if err := s.Commit(b); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
	if closeIt {
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	// closeIt == false 时直接丢弃 *Store，模拟进程崩溃。
	return walSize(t, dir)
}

// assertAtomic 断言对每个批次：全部键值正确可见，或一个键都不可见。
func assertAtomic(t *testing.T, s *Store, when string) {
	t.Helper()
	for bi, b := range batches {
		present := 0
		for k, want := range b {
			got, ok := s.Get(k)
			if ok {
				present++
				if got != want {
					t.Fatalf("%s: batch %d key %s = %q, want %q", when, bi, k, got, want)
				}
			}
		}
		if present != 0 && present != len(b) {
			t.Fatalf("%s: batch %d partially visible: %d/%d", when, bi, present, len(b))
		}
	}
}

func truncateWAL(t *testing.T, dir string, size int64) {
	t.Helper()
	p := filepath.Join(dir, walFileName)
	f, err := os.OpenFile(p, os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestTruncateEveryByte 把 wal.log 从 0 到完整长度逐字节截断，
// 每次重新 Open，原子可见性不变量必须恒成立。
func TestTruncateEveryByte(t *testing.T) {
	src, _ := openTempStore(t)
	src.Close()

	full := writeBatches(t, src.dir, true)
	for n := int64(0); n <= full; n++ {
		truncateWAL(t, src.dir, n)
		s := reopen(t, src.dir)
		assertAtomic(t, s, "truncated")
		// 恢复时尾部垃圾必须被切掉：文件长度不能增长，
		// 且之后的追加必须接在干净边界上。
		if got := walSize(t, src.dir); got > n {
			t.Fatalf("truncate %d: wal grew to %d after open", n, got)
		}
		if err := s.Commit(map[string]string{"probe": "p"}); err != nil {
			t.Fatalf("commit after recovery: %v", err)
		}
		s.Close()

		// 再 Open 一次，验证恢复后追加的记录也能正确回放。
		s2 := reopen(t, src.dir)
		assertAtomic(t, s2, "re-reopen")
		if v, ok := s2.Get("probe"); !ok || v != "p" {
			t.Fatalf("truncate %d: probe lost", n)
		}
		s2.Close()

		// 重建完整 WAL 供下一个长度使用。
		if err := os.Remove(filepath.Join(src.dir, walFileName)); err != nil {
			t.Fatal(err)
		}
		full = writeBatches(t, src.dir, true)
	}
}

// TestDropStoreWithoutClose 模拟真正崩溃：Commit 返回后不 Close，
// 直接截断文件再恢复。
func TestDropStoreWithoutClose(t *testing.T) {
	s, dir := openTempStore(t)
	s.Close()
	full := writeBatches(t, dir, false) // 不关闭，直接丢弃

	backup, err := os.ReadFile(filepath.Join(dir, walFileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, cut := range []int64{0, full / 3, full - 1, full} {
		if err := os.WriteFile(filepath.Join(dir, walFileName), backup, 0o644); err != nil {
			t.Fatal(err)
		}
		truncateWAL(t, dir, cut)
		got := reopen(t, dir)
		assertAtomic(t, got, "dropped")
		// 完整长度：所有已确认批次必须全部可见（已确认即持久）。
		if cut == full {
			for _, b := range batches {
				for k, want := range b {
					if v, ok := got.Get(k); !ok || v != want {
						t.Fatalf("cut=full: %s = %q,%v want %q", k, v, ok, want)
					}
				}
			}
		}
		got.Close()
	}
}

// TestTornCheckpoint 检查点写到一半（rename 之前崩溃）：WAL 完整，不能丢数据；
// 检查点文件损坏时也必须忽略并回退到纯 WAL 恢复，不能拒绝启动。
func TestTornCheckpoint(t *testing.T) {
	s, dir := openTempStore(t)
	if err := s.Commit(map[string]string{"a": "1", "b": "2"}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// 场景 1：rename 之前崩溃，只留下半个临时文件，WAL 完整。
	if err := os.WriteFile(filepath.Join(dir, ckptTmpName), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := reopen(t, dir)
	va, oka := got.Get("a")
	vb, okb := got.Get("b")
	if got.Len() != 2 || !oka || !okb || va != "1" || vb != "2" {
		t.Fatalf("half tmp checkpoint lost WAL data: %v", got.data)
	}
	got.Close()

	// 场景 2：checkpoint.dat 损坏（半截），但 WAL 完整，同样必须能启动且不丢数据。
	if err := os.WriteFile(filepath.Join(dir, ckptFileName), []byte("KVR1\x05"), 0o644); err != nil {
		t.Fatal(err)
	}
	got = reopen(t, dir)
	defer got.Close()
	if v, ok := got.Get("a"); got.Len() != 2 || !ok || v != "1" {
		t.Fatalf("corrupt checkpoint + full WAL lost data: %v", got.data)
	}
	// 恢复后继续提交，再重启，新旧数据都正确（恢复不破坏）。
	if err := got.Commit(map[string]string{"c": "3"}); err != nil {
		t.Fatal(err)
	}
	got.Close()
	got2 := reopen(t, dir)
	defer got2.Close()
	if v, ok := got2.Get("c"); got2.Len() != 3 || !ok || v != "3" {
		t.Fatalf("post-recovery commit lost: %v", got2.data)
	}
}

func truncateFile(t *testing.T, p string, size int64) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
}
