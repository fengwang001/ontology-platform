// Package view 维护分组聚合视图：变更经校验与版本判定后落日志，
// 再以 Apply → Recompute → Commit 三阶段应用到内存状态。
package view

import (
	"errors"
	"sync"

	"ontology/agg"
	"ontology/change"
	"ontology/journal"
)

// Stage 标识三阶段应用的崩溃注入点。
type Stage int

const (
	StageApply Stage = iota + 1
	StageRecompute
	StageCommit
)

// ErrStaleVersion 表示版本回退或同版本不同内容的乱序变更。
var ErrStaleVersion = errors.New("view: stale or regressed version")

type record struct {
	group string
	value float64
}

type group struct {
	aggs    []agg.Aggregator
	members map[uint64]float64
}

// AggStats 是单个聚合器的重算统计快照。
type AggStats struct {
	Recomputes   int64
	MemberAccess int64
	MaxAccess    int64
}

// Stats 是视图统计快照；底层计数器是 View 的非导出字段。
type Stats struct {
	PerAgg   map[string]AggStats
	Rejected int64
	Applied  int64
}

// View 是分组聚合视图，并发安全。
type View struct {
	mu          sync.RWMutex
	groups      map[string]*group
	records     map[uint64]record
	lastVersion uint64
	lastChange  change.Change
	hasLast     bool
	rejected    int64
	applied     int64
	stats       map[string]*AggStats
	jw          *journal.Writer
	hook        func(Stage)
}

func newView(jw *journal.Writer, hook func(Stage)) *View {
	return &View{
		groups:  map[string]*group{},
		records: map[uint64]record{},
		stats: map[string]*AggStats{
			"count": {}, "sum": {}, "min": {}, "max": {}, "distinct": {},
		},
		jw:   jw,
		hook: hook,
	}
}

// NewMem 返回不落地日志的内存视图。
func NewMem() *View { return newView(nil, nil) }

// SetHook 注入阶段钩子（崩溃注入用），须在无并发时设置。
func (v *View) SetHook(h func(Stage)) { v.hook = h }

// Apply 应用一条变更：校验 → 版本判定 → 落日志 → 三阶段内存应用。
func (v *View) Apply(c change.Change) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := c.Validate(); err != nil {
		v.rejected++
		return err
	}
	if v.hasLast {
		switch {
		case c.Version > v.lastVersion:
		case c.Version == v.lastVersion && c.Equal(v.lastChange):
			return nil // 幂等重放：状态不变，不再落盘
		default:
			v.rejected++
			return ErrStaleVersion
		}
	}
	if v.jw != nil {
		if err := v.jw.Append(c); err != nil {
			return err
		}
	}
	v.applyMem(c)
	return nil
}

// applyMem 执行三阶段内存应用，调用方须持写锁。
func (v *View) applyMem(c change.Change) {
	old, existed := v.records[c.ID]
	removing := existed && (c.Op == change.OpDelete || c.Op == change.OpUpdate)
	// —— Apply 阶段：成员表 + 增量聚合 ——
	if removing {
		v.unlink(c.ID, old)
	}
	if c.Op == change.OpInsert || c.Op == change.OpUpdate {
		v.link(c)
	}
	if v.hook != nil {
		v.hook(StageApply)
	}
	// —— Recompute 阶段：仅对声明需要成员的聚合器例外触发 ——
	if removing {
		v.recompute(old)
	}
	// —— Commit 阶段：推进版本 ——
	if v.hook != nil {
		v.hook(StageCommit)
	}
	v.lastVersion, v.lastChange, v.hasLast = c.Version, c, true
	v.applied++
}

// unlink 摘除一条成员并对可增量撤回的聚合器直接 Sub。
func (v *View) unlink(id uint64, r record) {
	g := v.groups[r.group]
	delete(g.members, id)
	delete(v.records, id)
	for _, a := range g.aggs {
		if a.IncrementalDelete() {
			a.Sub(r.value)
		}
	}
}

// link 加入一条成员并对全部聚合器增量 Add。
func (v *View) link(c change.Change) {
	key := *c.Group
	g := v.groups[key]
	if g == nil {
		g = &group{aggs: agg.All(), members: map[uint64]float64{}}
		v.groups[key] = g
	}
	g.members[c.ID] = c.Value
	v.records[c.ID] = record{group: key, value: c.Value}
	for _, a := range g.aggs {
		a.Add(c.Value)
	}
}

// recompute 处理删除的例外路径：只重算声明需要成员且被删除值
// 命中敏感条件的聚合器；删空组整体消失。
func (v *View) recompute(old record) {
	g := v.groups[old.group]
	if g == nil {
		return
	}
	if len(g.members) == 0 {
		delete(v.groups, old.group)
		return
	}
	hooked := false
	for _, a := range g.aggs {
		if a.IncrementalDelete() || !a.RemoveCausesRecompute(old.value) {
			continue
		}
		members := make([]float64, 0, len(g.members))
		for _, val := range g.members {
			members = append(members, val)
		}
		a.Recompute(members)
		st := v.stats[a.Name()]
		st.Recomputes++
		st.MemberAccess += int64(len(members))
		if int64(len(members)) > st.MaxAccess {
			st.MaxAccess = int64(len(members))
		}
		if !hooked && v.hook != nil {
			hooked = true
			v.hook(StageRecompute)
		}
	}
}
