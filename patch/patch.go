// Package patch 应用 unified diff（偏移查找、原子拒绝、反向），并提供多文档并发存储。
package patch

import (
	"errors"

	"ontology/udiff"
)

var (
	// ErrContext：窗口内找不到能精确匹配的位置。
	ErrContext = errors.New("patch context does not match")
	// ErrFuzz：最近匹配也超出偏移范围。
	ErrFuzz = errors.New("no match within fuzz range")
)

// ApplyError 指出第几个 hunk 失败（Hunk 为 0 基）及其类别。
type ApplyError struct {
	Hunk int
	Err  error
}

func (e *ApplyError) Error() string { return "" }
func (e *ApplyError) Unwrap() error { return e.Err }

// Options 控制应用：Fuzz 为记录位置上下查找的行数。
type Options struct{ Fuzz int }

// Apply 原子地把 p 应用到 a；任一 hunk 失败则返回错误且不产生修改。
func Apply(a []byte, p *udiff.Patch, opt Options) ([]byte, error) { return nil, nil }

// Reverse 返回反向补丁（b→a）。
func Reverse(p *udiff.Patch) *udiff.Patch { return nil }

// Entry 是存储中的一个文档版本。
type Entry struct {
	Text    []byte
	Version int64
}

// Store 是多文档内存存储；Apply 为原子读改写，Log 记录成功提交。
type Store struct{ m map[string]Entry }

// NewStore 创建空存储。
func NewStore() *Store { return &Store{m: map[string]Entry{}} }

// Commit 是提交日志的一条记录。
type Commit struct {
	Doc     string
	Version int64
	Text    []byte
}
