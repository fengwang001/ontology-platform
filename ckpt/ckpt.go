// Package ckpt 实现检查点的原子写出与恢复：保留最近两份，损坏自动回退。
package ckpt

import (
	"errors"
	"os"
	"path/filepath"

	"ontology/sink"
)

// Store 是目录内轮换的双份检查点存储。
type Store struct {
	dir   string
	files [2]string
	next  int
}

// New 创建存储；文件名为 ckpt.1 / ckpt.2。
func New(dir string) *Store {
	return &Store{
		dir:   dir,
		files: [2]string{filepath.Join(dir, "ckpt.1"), filepath.Join(dir, "ckpt.2")},
		next:  0,
	}
}

// Save 原子写入较新槽位并轮换（始终保留最近两份）。
func (s *Store) Save(st *sink.State) error {
	data, err := sink.Encode(st)
	if err != nil {
		return err
	}
	path := s.files[s.next]
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	s.next ^= 1
	return nil
}

// Result 是恢复结论。
type Result struct {
	State    *sink.State
	FellBack bool // 较新检查点损坏、回退到了另一份完好检查点
	Missing  bool // 两份都不存在（首次运行）
}

// Recover 校验两份检查点：取 offset 最大的完好者；若存在损坏文件而
// 另一份完好可用，标记 FellBack。
func (s *Store) Recover() (*Result, error) {
	type cand struct {
		st      *sink.State
		corrupt bool
		exists  bool
		off     int64
	}
	cands := make([]cand, 2)
	for i, path := range s.files {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		cands[i].exists = true
		st, derr := sink.Decode(data)
		if derr != nil {
			cands[i].corrupt = true
			continue
		}
		cands[i].st = st
		cands[i].off = st.Offset
	}
	var anyExists, anyCorrupt bool
	var best *sink.State
	for i := range cands {
		anyExists = anyExists || cands[i].exists
		anyCorrupt = anyCorrupt || cands[i].corrupt
		if cands[i].st != nil && (best == nil || cands[i].off > best.Offset) {
			best = cands[i].st
		}
	}
	if !anyExists {
		return &Result{Missing: true, State: &sink.State{Groups: map[string]sink.Group{}}}, nil
	}
	if best == nil {
		return nil, errors.New("ckpt: both checkpoints are corrupt")
	}
	return &Result{State: best, FellBack: anyCorrupt}, nil
}
