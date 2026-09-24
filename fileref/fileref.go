// Package fileref 文件存储（内存模拟）与引用计数：每个文件被多少个现存快照
// 引用，提交时加引用，过期时减引用并物理删除归零的文件；文件名永不复用。
// 依赖 snapchain；自身不加锁，由上层（api）串行化。
package fileref

import (
	"sort"

	"ontology/snapchain"
)

// Store 模拟文件存储并维护引用计数。
type Store struct {
	files   map[string]bool // 文件存储（内存集合模拟）
	refs    map[string]int  // 每个文件被现存快照引用的次数
	seen    map[string]bool // 曾出现过的文件名（含已删除，永不复用）
	visited int             // 最近一次 Expire 访问的快照数与文件引用数之和
}

func (s *Store) init() {
	if s.files == nil {
		s.files = map[string]bool{}
		s.refs = map[string]int{}
		s.seen = map[string]bool{}
	}
}

// Seen 报告文件名是否曾出现过（用于「永不复用」判定）。
func (s *Store) Seen(f string) bool { return s.seen[f] }

// Register 提交时登记：add 写入存储并登记名字，新快照引用的每个文件计数 +1。
func (s *Store) Register(add, snapFiles []string) {
	s.init()
	for _, f := range add {
		s.files[f] = true
		s.seen[f] = true
	}
	for _, f := range snapFiles {
		s.refs[f]++
	}
}

// ApplyExpire 对每个过期快照的每个文件引用计数 -1，归零即物理删除；
// 返回本次删除的文件名（升序）。只访问过期快照，不扫描保留快照。
func (s *Store) ApplyExpire(expired []snapchain.Snapshot) []string {
	s.init()
	s.visited = 0
	del := []string{}
	for _, sn := range expired {
		s.visited++
		for _, f := range sn.Files {
			s.visited++
			s.refs[f]--
			if s.refs[f] == 0 {
				delete(s.refs, f)
				delete(s.files, f)
				del = append(del, f)
			}
		}
	}
	sort.Strings(del)
	return del
}

// Files 返回存储中的文件名（升序）。
func (s *Store) Files() []string {
	s.init()
	out := make([]string, 0, len(s.files))
	for f := range s.files {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}
