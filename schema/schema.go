// Package schema 维护有序列版本列表、三种演进操作与永不复用的列 ID 分配。
package schema

import (
	"errors"
	"slices"
	"sync"
)

// Column 是某版本里的一列；ID 全局唯一且永不复用。
type Column struct {
	ID      int
	Name    string
	Default string
}

type Kind int

const (
	OpAdd Kind = iota
	OpDrop
	OpRename
)

// Op 是一条演进操作：Add 用 Name/Default，Drop 用 Name，Rename 用 Name/NewName。
type Op struct {
	Kind    Kind
	Name    string
	NewName string
	Default string
}

func Add(name, def string) Op   { return Op{Kind: OpAdd, Name: name, Default: def} }
func Drop(name string) Op       { return Op{Kind: OpDrop, Name: name} }
func Rename(old, new string) Op { return Op{Kind: OpRename, Name: old, NewName: new} }

var (
	ErrInvalidOp       = errors.New("schema: invalid evolution op")
	ErrTooManyVersions = errors.New("schema: max versions exceeded")
)

// Version 是一个不可变的 schema 版本，pos 为预建的「列 ID → 位置」索引。
type Version struct {
	N    int
	Cols []Column
	pos  map[int]int
}

// Pos 返回列 ID 在本版本中的位置。
func (v Version) Pos(id int) (int, bool) { p, ok := v.pos[id]; return p, ok }

func makeVersion(n int, cols []Column) Version {
	pos := make(map[int]int, len(cols))
	for i, c := range cols {
		pos[c.ID] = i
	}
	return Version{N: n, Cols: cols, pos: pos}
}

// History 是版本历史；版本一旦创建不可变，Evolve 只追加。
type History struct {
	mu       sync.RWMutex
	versions []Version
	maxID    int
	maxVers  int
}

// New 创建版本 1，列 ID 按顺序分配 1..n。
func New(cols []Column, maxVersions int) (*History, error) {
	if len(cols) == 0 || maxVersions < 1 {
		return nil, ErrInvalidOp
	}
	seen := map[string]bool{}
	vc := make([]Column, len(cols))
	for i, c := range cols {
		if c.Name == "" || seen[c.Name] {
			return nil, ErrInvalidOp
		}
		seen[c.Name] = true
		vc[i] = Column{ID: i + 1, Name: c.Name, Default: c.Default}
	}
	return &History{versions: []Version{makeVersion(1, vc)}, maxID: len(cols), maxVers: maxVersions}, nil
}

func byName(cols []Column, name string) int {
	return slices.IndexFunc(cols, func(c Column) bool { return c.Name == name })
}

// Evolve 以最新版本为起点按序应用 ops，全部合法才提交；失败不留痕。
func (h *History) Evolve(ops []Op) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(ops) == 0 {
		return 0, ErrInvalidOp
	}
	if len(h.versions)+1 > h.maxVers {
		return 0, ErrTooManyVersions
	}
	cur := h.versions[len(h.versions)-1]
	cols := slices.Clone(cur.Cols)
	maxID := h.maxID
	for _, op := range ops {
		switch op.Kind {
		case OpAdd:
			if op.Name == "" || byName(cols, op.Name) >= 0 {
				return 0, ErrInvalidOp
			}
			maxID++
			cols = append(cols, Column{ID: maxID, Name: op.Name, Default: op.Default})
		case OpDrop:
			i := byName(cols, op.Name)
			if i < 0 {
				return 0, ErrInvalidOp
			}
			cols = slices.Delete(cols, i, i+1)
		case OpRename:
			i := byName(cols, op.Name)
			if i < 0 || op.NewName == "" || byName(cols, op.NewName) >= 0 {
				return 0, ErrInvalidOp
			}
			cols[i].Name = op.NewName
		default:
			return 0, ErrInvalidOp
		}
	}
	if len(cols) == 0 {
		return 0, ErrInvalidOp
	}
	h.versions = append(h.versions, makeVersion(cur.N+1, cols))
	h.maxID = maxID
	return cur.N + 1, nil
}

// Snapshot 原子地返回最新版本与指定版本，保证解码基于同一时刻的读 schema。
func (h *History) Snapshot(n int) (latest, src Version, ok bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	latest = h.versions[len(h.versions)-1]
	if n < 1 || n > len(h.versions) {
		return latest, Version{}, false
	}
	return latest, h.versions[n-1], true
}

// Latest 返回最新版本。
func (h *History) Latest() Version {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.versions[len(h.versions)-1]
}
