// Package overlay 实现一层只读下层与一层可写上层的联合目录视图。
//
// 合并视图的解析规则：
//   - 根总是目录。
//   - 上层记录为白障时该路径不存在。
//   - 上层记录为文件时该路径是该文件。
//   - 上层记录为不透明目录时该路径是只含上层内容的目录。
//   - 上层记录为普通目录时该路径是目录，子项为上层子项与
//     下层子项（仅当该路径下层可达）的并集，同名项上层优先。
//   - 上层无记录时，路径下层可达则取下层条目，否则不存在。
//
// 路径 q 下层可达当且仅当下层存在 q 且 q 的每个真祖先在上层
// 都不是不透明目录。
package overlay

import (
	"errors"
	"strings"
	"sync"
)

// 可被 errors.Is 区分的拒绝原因。
var (
	ErrInvalidPath = errors.New("overlay: 非法路径")
	ErrNotFound    = errors.New("overlay: 未找到")
	ErrNotDir      = errors.New("overlay: 不是目录")
	ErrIsDir       = errors.New("overlay: 是目录")
	ErrExist       = errors.New("overlay: 已存在")
	ErrNotEmpty    = errors.New("overlay: 目录非空")
	ErrCrossLayer  = errors.New("overlay: 跨层改名")
	ErrRenameSelf  = errors.New("overlay: 移入自身")
)

// recKind 是上层记录的种类。
type recKind int

const (
	recFile recKind = iota
	recDir
	recOpaque
	recWhiteout
)

// record 是上层在单条路径上的记录。
type record struct {
	kind    recKind
	content []byte
}

// EntryType 是 Lookup 返回的条目类型。
type EntryType int

const (
	EntryFile EntryType = iota
	EntryDir
)

// Entry 是 Lookup 的结果。
type Entry struct {
	Type    EntryType
	Content []byte
}

// Record 是 Upper 返回的单条上层记录。
type Record struct {
	Path    string
	Kind    string // "file"、"dir"、"opaque" 或 "whiteout"
	Content []byte // 仅文件有意义
}

// FS 是联合目录视图，所有方法可并发调用。
type FS struct {
	mu    sync.RWMutex
	lower map[string]lowerEntry
	upper map[string]record
}

type lowerEntry struct {
	isDir   bool
	content []byte
}

// New 由下层映射构造联合视图。lower 的键不含前导斜杠，各段非空
// 且不为 "." 或 ".."；以 "/" 结尾的键表示目录（值被忽略），否则
// 为文件。每个键的所有上级目录必须也以目录键出现，同一路径不得
// 同时作为文件键与目录键出现，否则整体拒绝（返回错误）。
func New(lower map[string][]byte) (*FS, error) {
	fs := &FS{
		lower: make(map[string]lowerEntry),
		upper: make(map[string]record),
	}
	for key, val := range lower {
		isDir := strings.HasSuffix(key, "/")
		p := strings.TrimSuffix(key, "/")
		if !validPath(p) {
			return nil, ErrInvalidPath
		}
		if _, dup := fs.lower[p]; dup {
			return nil, ErrInvalidPath
		}
		if isDir {
			fs.lower[p] = lowerEntry{isDir: true}
		} else {
			fs.lower[p] = lowerEntry{content: append([]byte(nil), val...)}
		}
	}
	for p := range fs.lower {
		for a := parent(p); a != ""; a = parent(a) {
			e, ok := fs.lower[a]
			if !ok || !e.isDir {
				return nil, ErrInvalidPath
			}
		}
	}
	return fs, nil
}

// validPath 校验非空路径：各段非空且不为 "." 或 ".."。
func validPath(p string) bool {
	if p == "" {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// validOpPath 校验操作路径；空串表示根，仅在 allowRoot 时合法。
func validOpPath(p string, allowRoot bool) bool {
	if p == "" {
		return allowRoot
	}
	return validPath(p)
}

func parent(p string) string {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return ""
	}
	return p[:i]
}

// ancestors 返回 p 的全部真祖先，自根向下排列。
func ancestors(p string) []string {
	var out []string
	for a := parent(p); a != ""; a = parent(a) {
		out = append(out, a)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}
