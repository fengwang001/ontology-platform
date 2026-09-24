// Package view 在 dag 之上维护视图值、脏标记、失效传播与按拓扑序重算。
package view

import (
	"errors"

	"ontology/dag"
)

// ErrUnresolved 表示 Recompute 时脏视图仍有未注册的依赖。
var ErrUnresolved = errors.New("view: unresolved dependency")

type view struct {
	deps  []string
	fn    func(...int64) int64 // 基视图为 nil
	val   int64
	dirty bool
	has   bool // 是否已有值
}

// Store 是全部视图的内存状态。非并发安全，由上层 api 加锁。
type Store struct {
	g     *dag.Graph
	views map[string]*view
	last  int // 最近一次 Recompute 实际求值的视图个数（非导出，不进公开接口）
}

func New() *Store {
	return &Store{g: dag.New(), views: map[string]*view{}}
}

// Has 报告 name 是否已注册。
func (s *Store) Has(name string) bool { _, ok := s.views[name]; return ok }

// IsBase 报告 name 是否零依赖基视图（调用前须确认已注册）。
func (s *Store) IsBase(name string) bool { return len(s.views[name].deps) == 0 }

// Add 注册视图；非基视图初始置脏。成环时返回错误且状态不变。
func (s *Store) Add(name string, deps []string, fn func(...int64) int64) error {
	if err := s.g.Add(name, deps); err != nil {
		return err
	}
	s.views[name] = &view{deps: append([]string(nil), deps...), fn: fn, dirty: len(deps) > 0}
	return nil
}

// Set 给基视图置值，并把它连同下游传递闭包标记为脏（布尔标记，天然去重）。
func (s *Store) Set(name string, val int64) {
	v := s.views[name]
	v.val, v.has, v.dirty = val, true, true
	for n := range s.g.Closure(name) {
		s.views[n].dirty = true
	}
}

// Get 读取视图当前值；ok 报告是否已有值。
func (s *Store) Get(name string) (int64, bool) {
	v := s.views[name]
	return v.val, v.has
}

// Recompute 按拓扑序重算全部脏视图。任一脏视图有未解析依赖或子图成环
// 则整体失败，状态不变。同一轮里每个视图至多求值一次。
func (s *Store) Recompute() error {
	dirty := map[string]bool{}
	for n, v := range s.views {
		if v.dirty {
			dirty[n] = true
		}
	}
	for n := range dirty { // 先校验，失败不留痕
		for _, d := range s.views[n].deps {
			if !s.g.Has(d) {
				return ErrUnresolved
			}
		}
	}
	order, err := s.g.Topo(dirty)
	if err != nil {
		return err
	}
	s.last = 0
	for _, n := range order {
		v := s.views[n]
		if v.fn == nil { // 基视图：值只能来自 Set，不求值
			v.dirty = false
			continue
		}
		args := make([]int64, len(v.deps))
		for i, d := range v.deps {
			args[i] = s.views[d].val
		}
		v.val, v.has, v.dirty = v.fn(args...), true, false
		s.last++
	}
	return nil
}
