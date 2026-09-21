package walstore

import (
	"os"
	"testing"
)

// openFDs 返回当前进程打开的文件描述符数量。
// 通过 /proc/self/fd 观测，不依赖 ulimit 的具体取值。
func openFDs(t *testing.T) int {
	t.Helper()
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("cannot read /proc/self/fd: %v", err)
	}
	return len(ents)
}

// TestCheckpointDoesNotLeakFDs 反复执行 Checkpoint，断言进程
// 打开的 fd 数不随检查点次数增长。旧实现中 syncDir 每次
// os.Open(dir) 后从不 Close，每次 Checkpoint 泄漏 2 个 fd，
// 长跑后必然触发 "too many open files"。
func TestCheckpointDoesNotLeakFDs(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.Commit(map[string]string{"k": "v"}); err != nil {
		t.Fatal(err)
	}

	// 先跑几轮预热（让运行时缓存等一次性分配稳定下来），
	// 再记录基线。
	for i := 0; i < 5; i++ {
		if err := s.Checkpoint(); err != nil {
			t.Fatal(err)
		}
	}
	before := openFDs(t)

	const rounds = 100
	for i := 0; i < rounds; i++ {
		if err := s.Checkpoint(); err != nil {
			t.Fatal(err)
		}
	}
	after := openFDs(t)

	// 每次 Checkpoint 至多允许常数级抖动；旧实现这里会
	// 稳定增长 2*rounds 个 fd。
	if after > before+2 {
		t.Fatalf("fd leak: before=%d after=%d (+%d after %d checkpoints)",
			before, after, after-before, rounds)
	}
}
