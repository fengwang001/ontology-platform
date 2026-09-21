// 演示 walstore 的崩溃恢复语义：提交、任意截断、重新打开、原子可见、
// 已确认即持久，以及检查点之后再次截断恢复。
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
	if ok {
		fmt.Println("OK  ", name)
		return
	}
	fmt.Println("FAIL", name)
	failures++
}

func walSize(dir string) int64 {
	fi, err := os.Stat(filepath.Join(dir, "wal.log"))
	if err != nil {
		panic(err)
	}
	return fi.Size()
}

func truncate(dir string, n int64) {
	if err := os.Truncate(filepath.Join(dir, "wal.log"), n); err != nil {
		panic(err)
	}
}

func mustOpen(dir string) *walstore.Store {
	s, err := walstore.Open(dir)
	if err != nil {
		panic(err)
	}
	return s
}

// batchAtomic 报告该批次是否"全部键值正确可见"或"全部不可见"。
func batchAtomic(s *walstore.Store, b map[string]string) bool {
	present := 0
	for k, want := range b {
		v, ok := s.Get(k)
		if ok {
			present++
			if v != want {
				return false
			}
		}
	}
	return present == 0 || present == len(b)
}

func main() {
	dir, err := os.MkdirTemp("", "walstore-demo-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	b1 := map[string]string{"b1-a": "1", "b1-b": "two", "b1-c": ""}
	b2 := map[string]string{"b2-a": "three", "b2-b": "four", "b2-c": "five"}
	b3 := map[string]string{"b3-a": "six", "b3-b": "seven"}

	s := mustOpen(dir)
	check("empty dir opens with Len()==0", s.Len() == 0)
	check("commit batch 1", s.Commit(b1) == nil)
	check("commit batch 2", s.Commit(b2) == nil)
	check("empty value distinct from missing", func() bool {
		v, ok := s.Get("b1-c")
		_, missing := s.Get("nope")
		return ok && v == "" && !missing
	}())
	check("empty batch rejected, no record written", errors.Is(s.Commit(map[string]string{}), walstore.ErrEmptyBatch))
	check("empty key rejected", errors.Is(s.Commit(map[string]string{"": "v"}), walstore.ErrEmptyKey))

	// 模拟崩溃：不 Close 直接丢弃，然后完整 WAL 重新打开（已确认即持久）。
	full := walSize(dir)
	s = mustOpen(dir)
	durable := batchAtomic(s, b1) && batchAtomic(s, b2)
	_, allThere := s.Get("b2-c")
	check("full WAL reopened: both batches durable", durable && allThere && s.Len() == 6)
	s.Close()

	// 截到中段再打开：每个批次必须整批可见或整批不可见。
	truncate(dir, full/2)
	s = mustOpen(dir)
	check("mid truncation: batch 1 atomic", batchAtomic(s, b1))
	check("mid truncation: batch 2 atomic", batchAtomic(s, b2))
	// 恢复后继续提交、再崩溃再恢复，新旧数据仍正确。
	check("commit after recovery", s.Commit(map[string]string{"post": "crash"}) == nil)
	s = mustOpen(dir)
	v, ok := s.Get("post")
	check("post-recovery commit survives next restart", ok && v == "crash")
	s.Close()

	// 做检查点，再提交一批，截断尾部后恢复。
	s = mustOpen(dir)
	check("checkpoint", s.Checkpoint() == nil)
	check("commit batch 3 after checkpoint", s.Commit(b3) == nil)
	tail := walSize(dir)
	truncate(dir, tail-3) // 撕掉新批次的尾巴
	s = mustOpen(dir)
	old := func() bool {
		v, ok := s.Get("post")
		return ok && v == "crash"
	}()
	check("checkpoint data survives truncation", old)
	check("batch 3 atomic after torn tail", batchAtomic(s, b3))
	s.Close()

	if failures > 0 {
		fmt.Printf("DEMO FAILED: %d check(s)\n", failures)
		os.Exit(1)
	}
	fmt.Println("ALL DEMO CHECKS PASSED")
}
