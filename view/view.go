package view

import (
	"errors"
	"math"
	"sync"

	"ontology/agg"
	"ontology/change"
)

var (
	// ErrVersionBackward 版本号小于已提交最大版本。
	ErrVersionBackward = errors.New("view: version went backward")
	// ErrMissingGroup 变更未携带分组键。
	ErrMissingGroup = errors.New("view: missing group key")
	// ErrNaNValue 变更取值为 NaN。
	ErrNaNValue = errors.New("view: NaN value")
)

// GroupResult 是一个组在全部聚合器上的结果与存在标记。
type GroupResult struct {
	Exists        bool
	Count         float64
	Sum           float64
	Min           float64
	Max           float64
	DistinctCount float64
}

// Stats 是非导出计数器的只读快照，用于证明重算是例外。
type Stats struct {
	Recomputes      int
	MembersAccessed int
}

type groupState struct {
	members []float64
	aggs    map[agg.Kind]agg.Aggregator
}

// View 是分组增量聚合视图。
type View struct {
	mu       sync.Mutex
	groups   map[string]*groupState
	kinds    []agg.Kind
	version  int64
	reject   int
	recomp   int
	membRead int

	// crash 为非 nil 时，处理到指定阶段即 panic 模拟崩溃。
	crash CrashPoint
}

// CrashPoint 标识三阶段中的崩溃注入点。
type CrashPoint int

const (
	CrashNone CrashPoint = iota
	CrashApply
	CrashRecompute
	CrashCommit
)

// New 创建包含 Count/Sum/Min/Max/DistinctCount 的视图。
func New() *View {
	return &View{
		groups: map[string]*groupState{},
		kinds:  []agg.Kind{agg.Count, agg.Sum, agg.Min, agg.Max, agg.DistinctCount},
	}
}

// SetCrashPoint 设置下一次 Apply 的崩溃注入点（用于恢复测试）。
func (v *View) SetCrashPoint(p CrashPoint) {
	v.mu.Lock()
	v.crash = p
	v.mu.Unlock()
}

// Version 返回已提交的最大版本号。
func (v *View) Version() int64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.version
}

// Rejected 返回被拒绝（缺组/NaN/乱序）的变更数。
func (v *View) Rejected() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.reject
}

// Stats 返回重算计数器快照。
func (v *View) Stats() Stats {
	v.mu.Lock()
	defer v.mu.Unlock()
	return Stats{Recomputes: v.recomp, MembersAccessed: v.membRead}
}

func (v *View) newGroup() *groupState {
	g := &groupState{aggs: map[agg.Kind]agg.Aggregator{}}
	for _, k := range v.kinds {
		g.aggs[k] = agg.New(k)
	}
	return g
}

// Apply 处理一条变更：版本校验 → Apply → Recompute → Commit。
func (v *View) Apply(c change.Change) (err error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if !c.GroupSet {
		v.reject++
		return ErrMissingGroup
	}
	if isNaN(c.Value) || (c.Op == change.Update && isNaN(c.NewValue)) {
		v.reject++
		return ErrNaNValue
	}
	if c.Version < v.version {
		v.reject++
		return ErrVersionBackward
	}
	if c.Version == v.version {
		return nil // 幂等重复：逐字段不变
	}

	defer func() {
		if r := recover(); r != nil {
			if r == crashSentinel {
				err = errCrashInjected
				return
			}
			panic(r)
		}
	}()

	affected := v.applyStage(c)
	if v.crash == CrashApply {
		v.crash = CrashNone
		panic(crashSentinel)
	}
	v.recomputeStage(affected)
	if v.crash == CrashRecompute {
		v.crash = CrashNone
		panic(crashSentinel)
	}
	if v.crash == CrashCommit {
		v.crash = CrashNone
		panic(crashSentinel)
	}
	v.version = c.Version
	return nil
}

var crashSentinel = struct{}{}

// ErrCrashInjected 表示在注入点模拟了一次崩溃（该变更未提交）。
var ErrCrashInjected = errors.New("view: crash injected before commit")

// applyStage 执行可增量的加/减，返回需要重算的组。
func (v *View) applyStage(c change.Change) map[string]bool {
	need := map[string]bool{}
	switch c.Op {
	case change.Insert:
		v.addRecord(c.Group, c.Value)
	case change.Delete:
		v.removeRecord(c.Group, c.Value, need)
	case change.Update:
		v.removeRecord(c.Group, c.Value, need)
		v.addRecord(c.NewGroup, c.NewValue)
	}
	return need
}

func (v *View) addRecord(group string, val float64) {
	g := v.groups[group]
	if g == nil {
		g = v.newGroup()
	v.groups[group] = g
	}
	g.members = append(g.members, val)
	for _, a := range g.aggs {
		a.Add(val)
	}
}

func (v *View) removeRecord(group string, val float64, need map[string]bool) {
	g := v.groups[group]
	if g == nil {
		return
	}
	idx := -1
	for i, m := range g.members {
		if eqVal(m, val) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	g.members = append(g.members[:idx], g.members[idx+1:]...)

	recomputeAny := false
	for _, k := range v.kinds {
		a := g.aggs[k]
		if a.Remove(val) {
			recomputeAny = true
		}
	}
	if len(g.members) == 0 {
		delete(v.groups, group)
		return
	}
	if recomputeAny {
		need[group] = true
	}
}

// recomputeStage 只重建被标记组，且只遍历该组成员。
func (v *View) recomputeStage(need map[string]bool) {
	for group := range need {
		g := v.groups[group]
		if g == nil {
			continue
		}
		v.recomp++
		v.membRead += len(g.members)
		for _, a := range g.aggs {
			if a.NeedsMembersOnDelete() {
				a.Recompute(g.members)
			}
		}
	}
}

func isNaN(f float64) bool { return math.IsNaN(f) }

func eqVal(x, y float64) bool {
	if x == 0 {
		x = 0
	}
	if y == 0 {
		y = 0
	}
	return x == y
}
