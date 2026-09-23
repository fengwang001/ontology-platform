// Package ckpt 负责检查点（已消费位置 + 中间聚合）的写出与恢复。
package ckpt

import (
	"errors"
	"io/fs"
)

// Snapshot 是一个检查点的完整内容。
type Snapshot struct {
	Pos    int64
	Groups map[string]int64
}

// State 是恢复结果。
type State struct {
	Snap     Snapshot
	FellBack bool
}

// ErrNone 表示目录中没有任何可用检查点。
var ErrNone = errors.New("ckpt: no usable checkpoint")

// Store 按两代轮换原子写检查点（骨架）。
type Store struct {
	dir string
}

// NewStore 在 dir 下管理检查点。
func NewStore(dir string) *Store { return &Store{dir: dir} }

// Save 原子写出一代新检查点。
func (st *Store) Save(s Snapshot) error { return nil }

// Load 读取最新完好检查点，损坏则回退上一代。
func (st *Store) Load() (State, error) { return State{}, ErrNone }

// 供实现使用的占位，确保 fs 被引用（实现时移除）。
var _ fs.FileMode = 0
