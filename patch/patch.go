package patch

import (
	"errors"

	"ontology/lines"
	"ontology/udiff"
)

var (
	// ErrContext 上下文/删除行无法精确匹配。
	ErrContext = errors.New("patch: context mismatch")
	// ErrOffset 偏移范围内找不到候选位置。
	ErrOffset = errors.New("patch: hunk outside fuzz offset")
)

// Error 指明失败的 hunk 序号（0 基）与原因类别。
type Error struct {
	Hunk  int
	Class error
}

func (e *Error) Error() string { return "" }

// Options 控制模糊应用。
type Options struct {
	Fuzz int
}

// Apply 原子应用补丁；任何 hunk 失败则返回原文本。
func Apply(a []byte, p *udiff.Patch, o Options) ([]byte, error) { return nil, nil }

// Reverse 交换补丁的新旧侧。
func Reverse(p *udiff.Patch) *udiff.Patch { return nil }

// Commit 是提交日志中的一条记录。
type Commit struct {
	ID    int
	OK    bool
	Patch *udiff.Patch
}

// Store 是多文档并发存储。
type Store struct {
	docs map[string]*doc
}

type doc struct {
	text []byte
	mu   chan struct{}
	ver  int
	log  []Commit
}

// NewStore 创建空存储。
func NewStore() *Store { return nil }

// Put 写入文档初始版本（版本 0）。
func (s *Store) Put(name string, text []byte) {}

// Apply 在一致快照上原子应用，成功版本加一并记日志。
func (s *Store) Apply(name string, p *udiff.Patch, o Options) (Commit, error) {
	return Commit{}, nil
}

// Get 返回当前文本与版本。
func (s *Store) Get(name string) ([]byte, int, bool) { return nil, 0, false }

// Log 返回提交日志（成功与失败均记录）。
func (s *Store) Log(name string) []Commit { return nil }

var _ = lines.Line{}
