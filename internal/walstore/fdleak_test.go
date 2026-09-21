package walstore

import (
	"os"
	"runtime"
	"testing"
)

// countOpenFDs 通过 /proc/self/fd 统计当前进程打开的 fd 数，
// 不依赖 ulimit 的具体取值，也不需要等待 GC 或 finalizer。
func countOpenFDs(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatalf("read /proc/self/fd: %v", err)
	}
	return len(entries)
}

// TestCheckpointDoesNotLeakFDs 反复执行 Checkpoint，断言进程打开的
// fd 数不随检查点次数增长。旧实现的 syncDir 打开目录后从不 Close，
// 每次 Checkpoint 泄漏 2 个 fd，本测试在该实现下必定失败。
func TestCheckpointDoesNotLeakFDs(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("需要 /proc/self/fd 来统计 fd 数量")
	}
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	if err := s.Commit(map[string]string{"k": "v"}); err != nil {
		t.Fatalf("commit: %v", err)
	}

	before := countOpenFDs(t)
	const rounds = 64
	for i := 0; i < rounds; i++ {
		if err := s.Checkpoint(); err != nil {
			t.Fatalf("checkpoint %d: %v", i, err)
		}
	}
	after := countOpenFDs(t)
	if after > before {
		t.Fatalf("Checkpoint 泄漏 fd：%d 轮后 fd 数从 %d 涨到 %d（泄漏 %d 个）",
			rounds, before, after, after-before)
	}
}
