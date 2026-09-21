// demo 实际演练 walstore 的崩溃恢复语义：提交批次、截断
// wal.log 模拟断电、重新 Open 验证原子可见与已确认即持久、
// 检查点后再截断再恢复。全部通过时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"ontology/internal/walstore"
)

var failures int

func check(name string, ok bool) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, name)
}

func visible(s *walstore.Store, batch map[string]string) int {
	n := 0
	for k, v := range batch {
		if got, ok := s.Get(k); ok && got == v {
			n++
		}
	}
	return n
}

func reopen(dir string) *walstore.Store {
	s, err := walstore.Open(dir)
	if err != nil {
		fmt.Printf("FAIL reopen: %v\n", err)
		os.Exit(1)
	}
	return s
}

func emptyValueOK(s *walstore.Store) bool {
	v, ok := s.Get("e")
	return ok && v == ""
}

func mustRead(path string) []byte {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Println("FAIL read wal:", err)
		os.Exit(1)
	}
	return b
}

// openFDs 返回当前进程打开的 fd 数，用于观测资源泄漏。
func openFDs() int {
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return -1
	}
	return len(ents)
}

func main() {
	dir, err := os.MkdirTemp("", "walstore-demo")
	if err != nil {
		fmt.Println("FAIL tempdir:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)
	wal := filepath.Join(dir, "wal.log")
	s := reopen(dir)
	check("open empty dir, Len==0", s.Len() == 0)
	check("empty batch rejected", errors.Is(s.Commit(map[string]string{}), walstore.ErrEmptyBatch))
	check("empty key rejected", errors.Is(s.Commit(map[string]string{"": "v"}), walstore.ErrEmptyKey))

	b1 := map[string]string{"a": "1", "b": "2"}
	b2 := map[string]string{"c": "3"}
	b3 := map[string]string{"d": "4", "e": ""}
	s.Commit(b1)
	s.Commit(b2)
	lenAfterB2 := len(mustRead(wal))
	s.Commit(b3)
	check("commit 3 batches, Len==5", s.Len() == 5)

	// 崩溃 1：wal.log 被截断到 b2 之后、b3 写了一半的位置。
	full := mustRead(wal)
	os.WriteFile(wal, full[:lenAfterB2+7], 0o644)
	s = reopen(dir)
	check("crash: b1 fully visible", visible(s, b1) == len(b1))
	check("crash: b2 fully visible", visible(s, b2) == len(b2))
	check("crash: torn b3 fully invisible", visible(s, b3) == 0)
	check("acked-is-durable, Len==3", s.Len() == 3)

	// 恢复后继续提交（b3 未被确认过，重新提交），再崩溃
	//（尾部带垃圾）再恢复。
	s.Commit(b3)
	b4 := map[string]string{"f": "6"}
	s.Commit(b4)
	tail := mustRead(wal)
	os.WriteFile(wal, append(tail, 0xDE, 0xAD), 0o644)
	s = reopen(dir)
	check("recover+commit: old data intact", visible(s, b1) == 2 && visible(s, b2) == 1)
	check("recover+commit: b3 now visible", visible(s, b3) == 2)
	check("recover+commit: b4 visible", visible(s, b4) == 1)

	// 检查点后把 wal.log 截断成垃圾，再恢复。
	check("checkpoint ok", s.Checkpoint() == nil)
	os.WriteFile(wal, []byte{0x57, 0x41, 0x4c, 0x31, 0x00}, 0o644)
	s = reopen(dir)
	check("checkpoint survives torn wal", s.Len() == 6)
	check("empty value distinct from missing", emptyValueOK(s))

	// 演练 fd 泄漏修复：反复做检查点，进程 fd 数应保持稳定
	//（修复前每次 Checkpoint 泄漏 2 个目录 fd）。
	fdBefore := openFDs()
	for i := 0; i < 200; i++ {
		if err := s.Checkpoint(); err != nil {
			fmt.Println("FAIL checkpoint loop:", err)
			os.Exit(1)
		}
	}
	fdAfter := openFDs()
	check(fmt.Sprintf("200 checkpoints: fds stable (%d -> %d)", fdBefore, fdAfter),
		fdBefore < 0 || fdAfter <= fdBefore+2)
	s.Close()

	if failures > 0 {
		fmt.Printf("%d check(s) FAILED\n", failures)
		os.Exit(1)
	}
	fmt.Println("all checks passed")
}
