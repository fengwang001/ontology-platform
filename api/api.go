// Package api 是对外门面：New、操作包装、Snapshot、Triggers、SelfCheck。
package api

import (
	"errors"
	"fmt"

	"ontology/win"
	"ontology/wmgr"
)

// Engine 是窗口状态复活器的对外入口。
type Engine struct {
	m *wmgr.Mgr
}

// New 构造引擎：T 为触发阈值（正），maxWindows 为窗口数上限。
func New(T, maxWindows int64) *Engine {
	return &Engine{m: wmgr.New(T, maxWindows)}
}

func (e *Engine) Ingest(id string, val int64) error { return e.m.Ingest(id, val) }
func (e *Engine) Purge(id string) error             { return e.m.Purge(id) }
func (e *Engine) Late(id string, val int64) error   { return e.m.Late(id, val) }
func (e *Engine) GC()                               { e.m.GC() }

// Snapshot 返回窗口的 agg/trg/state；ok=false 表示窗口不存在。
func (e *Engine) Snapshot(id string) (agg, trg int64, state win.State, ok bool) {
	return e.m.Snapshot(id)
}

// Triggers 返回累计触发器事件数。
func (e *Engine) Triggers() int64 { return e.m.Triggers() }

// SelfCheck 用内置操作序列核验四条不变量，全部通过返回 nil。
// 在独立的内部实例上运行，不影响引擎自身状态。
func SelfCheck() error {
	// 不变量 1：复活只服务一条（丢弃冻结历史）
	m := wmgr.New(10, 100)
	m.Ingest("w", 7)
	m.Ingest("w", 5) // agg=12 冻结于此
	m.Purge("w")
	m.Late("w", 3)
	if agg, _, _, _ := m.Snapshot("w"); agg != 3 {
		return fmt.Errorf("selfcheck 不变量1: 复活后 agg=%d, 期望 3", agg)
	}
	// 不变量 2：复活后抑制触发
	m.Late("w", 8) // agg=11 越过 T=10
	m.Ingest("w", 2)
	if _, trg, _, _ := m.Snapshot("w"); trg != 1 || m.Triggers() != 1 {
		return errors.New("selfcheck 不变量2: revived 窗口仍触发了触发器")
	}
	// 不变量 3：GC 保留 revived、删除 purged 未复活
	m.Ingest("v", 1)
	m.Purge("v")
	m.GC()
	if _, _, _, ok := m.Snapshot("w"); !ok {
		return errors.New("selfcheck 不变量3: GC 误删 revived 窗口")
	}
	if _, _, _, ok := m.Snapshot("v"); ok {
		return errors.New("selfcheck 不变量3: GC 未删 purged 未复活窗口")
	}
	// 不变量 4：失败不留痕
	before, _, _, _ := m.Snapshot("w")
	if m.Ingest("", 1) == nil || m.Ingest("w", -1) == nil || m.Late("ghost", 1) == nil {
		return errors.New("selfcheck 不变量4: 非法操作未被拒绝")
	}
	if after, _, _, _ := m.Snapshot("w"); after != before {
		return errors.New("selfcheck 不变量4: 被拒操作改变了状态")
	}
	return nil
}
