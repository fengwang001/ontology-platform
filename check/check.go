// Package check 实现 checkpoint 位点文件的原子读写与损坏判定。
// 位点是一个 8 字节 int64，存「下一个要分配的编号」。
package check

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// 可判定哨兵错误。
var (
	// ErrPersist 表示 checkpoint 持久化失败（写/ fsync / rename 任一环节）。
	ErrPersist = errors.New("check: persist checkpoint failed")
	// ErrCorrupt 表示 checkpoint 损坏（长度非 8 字节或非法值）。
	ErrCorrupt = errors.New("check: corrupt checkpoint")
)

// Store 管理 dir 下名为 checkpoint 的位点文件。
type Store struct {
	path string
	// lastRead 记录最近一次 Read 读取/解析的 checkpoint 记录条数。
	// 非导出，仅供包内测试核验「单个 int64 位点」而非追加日志。
	lastRead int
}

// Open 打开 dir 下的 checkpoint 存储；dir 为空报 ErrPersist。
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("%w: empty dir", ErrPersist)
	}
	return &Store{path: filepath.Join(dir, "checkpoint")}, nil
}

// Write 原子持久化 next：临时文件 + fsync + rename + fsync 目录。
func (s *Store) Write(next int64) error {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(next))
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPersist, err)
	}
	if _, err := f.Write(buf[:]); err != nil {
		f.Close()
		return fmt.Errorf("%w: %v", ErrPersist, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("%w: %v", ErrPersist, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("%w: %v", ErrPersist, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("%w: %v", ErrPersist, err)
	}
	if d, err := os.Open(filepath.Dir(s.path)); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

// Read 读取 checkpoint 重建 next。文件不存在视为全新（返回 0）；
// 长度非 8 字节或值为负报 ErrCorrupt，不瞎给号。
func (s *Store) Read() (int64, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.lastRead = 0
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrPersist, err)
	}
	if len(b) != 8 {
		return 0, fmt.Errorf("%w: length %d != 8", ErrCorrupt, len(b))
	}
	v := int64(binary.BigEndian.Uint64(b))
	if v < 0 {
		return 0, fmt.Errorf("%w: negative value %d", ErrCorrupt, v)
	}
	s.lastRead = 1
	return v, nil
}
