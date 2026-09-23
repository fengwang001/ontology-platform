// Package replay 提供只追加事件日志（分段 + 稀疏索引）与按序号范围回放。
package replay

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"ontology/segment"
	"ontology/sparse"
)

// ErrInvalidRange：from > to 的范围不可判定，直接报错。
var ErrInvalidRange = errors.New("replay: from > to")

// ErrClosed：日志已关闭仍要追加。
var ErrClosed = errors.New("replay: log closed")

type segMeta struct {
	path  string
	first uint64
	count uint64
}

// Log 是分段只追加日志。追加与回放可并发：回放看到一致前缀。
type Log struct {
	dir       string
	maxPerSeg int
	n         int

	mu      sync.Mutex
	w       *segment.Writer
	builder *sparse.Builder
	active  segMeta
	segs    []segMeta
	nextSeq uint64
	closed  bool
}

// Open 在 dir 下新建日志；maxPerSeg 为每段事件数上限，n 为索引锚点间隔。
func Open(dir string, maxPerSeg, n int) (*Log, error) {
	if maxPerSeg <= 0 || n <= 0 {
		return nil, fmt.Errorf("replay: maxPerSeg and n must be positive")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Log{dir: dir, maxPerSeg: maxPerSeg, n: n}, nil
}

// Append 追加一条事件，返回其序号。
func (l *Log) Append(payload []byte) (uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return 0, ErrClosed
	}
	if l.w == nil || l.w.Count() >= uint64(l.maxPerSeg) {
		if err := l.rotateLocked(); err != nil {
			return 0, err
		}
	}
	seq, err := l.w.Append(payload)
	if err != nil {
		return 0, err
	}
	l.active.count++
	l.nextSeq++
	return seq, nil
}

func (l *Log) rotateLocked() error {
	if err := l.finalizeLocked(); err != nil {
		return err
	}
	path := filepath.Join(l.dir, fmt.Sprintf("seg-%06d.dat", len(l.segs)))
	w, err := segment.Create(path, l.nextSeq)
	if err != nil {
		return err
	}
	l.builder = sparse.NewBuilder(l.n)
	w.OnEvent = l.builder.Observe
	l.w = w
	l.active = segMeta{path: path, first: l.nextSeq}
	return nil
}

func (l *Log) finalizeLocked() error {
	if l.w == nil {
		return nil
	}
	if err := l.w.Close(); err != nil {
		return err
	}
	idxPath := l.active.path[:len(l.active.path)-len(".dat")] + ".idx"
	if err := l.builder.Index().Save(idxPath); err != nil {
		return err
	}
	l.segs = append(l.segs, l.active)
	l.w = nil
	l.builder = nil
	return nil
}

// Close 收尾当前段（回填段头、写索引）。关闭后仍可回放。
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	return l.finalizeLocked()
}

// snapshot 返回已收尾段 + 当前活跃段（含已落盘条数）的一致快照。
func (l *Log) snapshot() []segMeta {
	l.mu.Lock()
	defer l.mu.Unlock()
	metas := make([]segMeta, 0, len(l.segs)+1)
	metas = append(metas, l.segs...)
	if l.w != nil {
		metas = append(metas, l.active)
	}
	return metas
}
