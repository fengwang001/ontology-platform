package store

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// Record 是一条已提交操作的持久化记录，包含重放所需的全部字段。
type Record struct {
	ObjectType string `json:"objectType"`
	PK         string `json:"pk"`
	Kind       OpKind `json:"kind"`
	Token      int64  `json:"token"`
	BizStart   int64  `json:"bizStart"`
	Seq        int64  `json:"seq"`
	Sys        int64  `json:"sys"`
	Payload    string `json:"payload,omitempty"`
}

// WAL 是先写日志：每条记录在内存提交前追加。
type WAL interface {
	Append(Record) error
}

// MemWAL 是内存 WAL，用于测试与重放验证。
type MemWAL struct {
	mu      sync.Mutex
	Records []Record
}

// Append 追加一条记录。
func (w *MemWAL) Append(r Record) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.Records = append(w.Records, r)
	return nil
}

// FileWAL 以 JSON Lines 形式把已提交操作持久化到文件。
type FileWAL struct {
	mu  sync.Mutex
	enc *json.Encoder
	f   *os.File
}

// NewFileWAL 创建（或追加到）一个 WAL 文件。
func NewFileWAL(path string) (*FileWAL, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &FileWAL{enc: json.NewEncoder(f), f: f}, nil
}

// Append 追加一条记录并落盘。
func (w *FileWAL) Append(r Record) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.enc.Encode(r); err != nil {
		return err
	}
	return w.f.Sync()
}

// Close 关闭底层文件。
func (w *FileWAL) Close() error { return w.f.Close() }

// Replay 用一组已提交记录重建 Store，得到的版本链与查询结果
// 与原序列完全一致（系统时间取自记录本身，不依赖时钟）。
func Replay(records []Record) (*Store, error) {
	s := New(NewFakeClock(0))
	for i, rec := range records {
		if err := s.applyCommitted(rec); err != nil {
			return nil, fmt.Errorf("重放第 %d 条记录失败: %w", i+1, err)
		}
	}
	return s, nil
}

// applyCommitted 不重校验地应用一条已提交记录，仅断言链式不变量。
func (s *Store) applyCommitted(rec Record) error {
	c := s.getOrCreateChain(chainKey(rec.ObjectType, rec.PK))
	c.mu.Lock()
	defer c.mu.Unlock()
	if want := int64(len(c.versions)) + 1; rec.Seq != want {
		return fmt.Errorf("版本号不连续: got v%d, want v%d", rec.Seq, want)
	}
	if len(c.versions) > 0 && rec.Sys <= c.lastSys {
		return fmt.Errorf("系统时间回退: %d <= %d", rec.Sys, c.lastSys)
	}
	v := Version{Seq: rec.Seq, Sys: rec.Sys, BizStart: rec.BizStart, Kind: rec.Kind, Payload: rec.Payload}
	c.versions = append(c.versions, v)
	c.idx.Add(rec.BizStart, rec.Sys, rec.Seq)
	c.lastSys = rec.Sys
	if len(c.versions) == 1 || rec.BizStart < c.minBiz {
		c.minBiz = rec.BizStart
	}
	return nil
}
