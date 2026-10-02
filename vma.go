package ontology

import (
	"errors"
	"sync"
)

var (
	ErrNoSpace    = errors.New("vma: no space")
	ErrTooMany    = errors.New("vma: too many VMAs")
	ErrExists     = errors.New("vma: mapping exists")
	ErrNoMem      = errors.New("vma: interval is not fully mapped")
	ErrMapped     = errors.New("vma: address is mapped")
	ErrSegv       = errors.New("vma: no downward-growing stack above address")
	ErrStackLimit = errors.New("vma: stack size limit exceeded")
	ErrNoRoom     = errors.New("vma: stack guard room unavailable")
)

const (
	Fixed     = 1
	NoReplace = 2
	GrowsDown = 4
)

type Source struct{ File, Off int64 }

type VMA struct {
	Start, End int64
	Perm       int
	GrowsDown  bool
	Anonymous  bool
	Source     Source
}

type Manager struct {
	mu       sync.RWMutex
	low      int64
	high     int64
	maxV     int
	guard    int64
	maxStack int64
	tree     vmaTree
	blocked  *blockTree
	visited  uint64
}

func New(low, high int64, maxV int, guard, maxStack int64) (*Manager, error) {
	if low < 1 || high <= low || high > 1<<40 || maxV < 1 || maxV > 1_000_000 ||
		guard < 1 || guard > 1<<20 || maxStack < 1 || maxStack > 1<<40 {
		return nil, errors.New("vma: invalid configuration")
	}
	return &Manager{low: low, high: high, maxV: maxV, guard: guard, maxStack: maxStack,
		blocked: newBlockTree(low, high)}, nil
}

func invalidArgument() error { return errors.New("vma: invalid argument") }

func (m *Manager) VMAs() []VMA { m.mu.RLock(); defer m.mu.RUnlock(); return m.tree.list() }
func (m *Manager) Count() int  { m.mu.RLock(); defer m.mu.RUnlock(); return m.tree.count() }

func (m *Manager) replaceAll(vmas []VMA) {
	m.tree = vmaTree{}
	m.blocked = newBlockTree(m.low, m.high)
	for _, v := range vmas {
		m.tree.insert(v)
		if v.End > v.Start {
			m.blocked.add(v.Start, v.End, 1)
		}
		if v.GrowsDown {
			guardStart := maxInt64(m.low, v.Start-m.guard)
			if v.Start > guardStart {
				m.blocked.add(guardStart, v.Start, 1)
			}
		}
	}
}

func (m *Manager) newVMA(start, end int64, perm int, flags int, file, off int64) VMA {
	v := VMA{Start: start, End: end, Perm: perm, GrowsDown: flags&GrowsDown != 0, Anonymous: file == 0}
	if file != 0 {
		v.Source = Source{File: file, Off: off}
	}
	return v
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
