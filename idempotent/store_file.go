package idempotent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// FileStore 把状态快照以“临时文件 + fsync + 原子 rename”方式落盘。
type FileStore struct {
	path string
}

// NewFileStore 创建指向 path 的文件存储。
func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

// Load 读取最近一次成功提交的快照；文件不存在时返回空快照。
func (f *FileStore) Load(ctx context.Context) (*snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(f.path)
	if errors.Is(err, fs.ErrNotExist) {
		return &snapshot{
			Version:    1,
			Watermarks: map[int]int64{},
			Duplicates: map[int]int64{},
			Results:    map[string]int64{},
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("idempotent: read snapshot %s: %w", f.path, err)
	}
	snap := &snapshot{}
	if err := json.Unmarshal(data, snap); err != nil {
		return nil, fmt.Errorf("idempotent: corrupt snapshot %s: %w", f.path, err)
	}
	if snap.Watermarks == nil {
		snap.Watermarks = map[int]int64{}
	}
	if snap.Duplicates == nil {
		snap.Duplicates = map[int]int64{}
	}
	if snap.Results == nil {
		snap.Results = map[string]int64{}
	}
	return snap, nil
}

// Commit 用一次原子替换把快照持久化。
func (f *FileStore) Commit(ctx context.Context, snap *snapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("idempotent: marshal snapshot: %w", err)
	}

	dir := filepath.Dir(f.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("idempotent: create state dir: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".snapshot-*.tmp")
	if err != nil {
		return fmt.Errorf("idempotent: create temp snapshot: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}

	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("idempotent: write snapshot: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("idempotent: fsync snapshot: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("idempotent: close snapshot: %w", err)
	}
	if err := os.Rename(tmpName, f.path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("idempotent: atomic rename snapshot: %w", err)
	}

	dirHandle, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("idempotent: open state dir: %w", err)
	}
	if err := dirHandle.Sync(); err != nil {
		_ = dirHandle.Close()
		return fmt.Errorf("idempotent: fsync state dir: %w", err)
	}
	if err := dirHandle.Close(); err != nil {
		return fmt.Errorf("idempotent: close state dir: %w", err)
	}
	return nil
}
