// Package sink 把聚合快照原子落盘，并管理临时文件。
package sink

import (
	"context"
	"errors"
)

// Sink 负责原子写出全量聚合快照。
type Sink struct {
	path string
}

// New 创建指向 path 的 Sink，并清理残留临时文件（骨架）。
func New(path string) (*Sink, error) { return &Sink{path: path}, nil }

// Commit 写临时文件 → fsync → 原子改名。
// beforeRename 非空时在改名前回调（故障注入）。
func (s *Sink) Commit(ctx context.Context, groups map[string]int64, beforeRename func() error) error {
	return nil
}

// Read 读回有效输出并校验（骨架）。
func (s *Sink) Read() ([]byte, error) { return nil, errors.New("sink: no output") }

// IsTmp 判断 name 是否为未完成临时文件名（骨架）。
func IsTmp(name string) bool { return false }
