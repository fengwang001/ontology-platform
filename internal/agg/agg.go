// Package agg 做内存分组聚合：sum/count，组数有硬上限，支持快照与恢复。
package agg

import (
	"errors"

	"ontology/internal/parse"
	"ontology/internal/stage"
)

// ErrTooManyGroups 在分组数超过硬上限时返回。
var ErrTooManyGroups = errors.New("agg: too many groups")

// Group 是某键的中间聚合。
type Group struct {
	Key   string `json:"k"`
	Sum   int64  `json:"s"`
	Count int64  `json:"c"`
}

// Aggregator 维护各分组中间态；非并发安全，由所属阶段单协程驱动。
type Aggregator struct {
	groups map[string]*Group
	maxG   int
	// OnQuiet 在每个屏障（静止点）到达时回调，seq 为屏障序号。
	OnQuiet func(seq int64, snap []Group)
	// CrashAt 若与屏障序号相等则返回崩溃错误（故障注入：聚合中途）。
	CrashAt int64
	// CrashErr 为注入错误；为 nil 时用 ErrCrashMid。
	CrashErr error
}

// ErrCrashMid 是聚合中途注入崩溃的哨兵错误。
var ErrCrashMid = errors.New("agg: injected crash mid-way")

// New 构造聚合器，maxGroups<=0 表示不限。
func New(maxGroups int, snap []Group) *Aggregator {
	a := &Aggregator{groups: map[string]*Group{}, maxG: maxGroups}
	for i := range snap {
		g := snap[i]
		a.groups[g.Key] = &g
	}
	return a
}

// Snapshot 返回按键升序的确定性快照。
func (a *Aggregator) Snapshot() []Group {
	out := make([]Group, 0, len(a.groups))
	for _, g := range a.groups {
		out = append(out, *g)
	}
	sortGroups(out)
	return out
}

func sortGroups(gs []Group) {
	for i := 1; i < len(gs); i++ {
		for j := i; j > 0 && gs[j-1].Key > gs[j].Key; j-- {
			gs[j-1], gs[j] = gs[j], gs[j-1]
		}
	}
}

// Work 是阶段工作函数：屏障触发静止点回调并透传。
func (a *Aggregator) Work(in stage.Msg[parse.Rec], emit func(stage.Msg[[]Group])) error {
	if in.Barrier {
		if a.CrashAt > 0 && in.Seq >= a.CrashAt {
			if a.CrashErr != nil {
				return a.CrashErr
			}
			return ErrCrashMid
		}
		snap := a.Snapshot()
		if a.OnQuiet != nil {
			a.OnQuiet(in.Seq, snap)
		}
		emit(stage.Msg[[]Group]{Seq: in.Seq, Barrier: true, V: snap})
		return nil
	}
	g, ok := a.groups[in.V.Key]
	if !ok {
		if a.maxG > 0 && len(a.groups) >= a.maxG {
			return ErrTooManyGroups
		}
		g = &Group{Key: in.V.Key}
		a.groups[in.V.Key] = g
	}
	g.Sum += in.V.Val
	g.Count++
	return nil
}
